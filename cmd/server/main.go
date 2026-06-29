package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	"go.uber.org/zap"

	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/handler"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/database"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
	"github.com/yintengching/playerledger/pkg/metrics"
	"github.com/yintengching/playerledger/pkg/ratelimit"
	"github.com/yintengching/playerledger/pkg/redis"
)

func main() {
	// ═══════════════════════════════════════════════════════
	// 1. 加載 Config（§4）
	// ═══════════════════════════════════════════════════════
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// ═══════════════════════════════════════════════════════
	// 2. 初始化 Logger（§5.2）
	// ═══════════════════════════════════════════════════════
	if err := logger.Init(cfg.Log, cfg.App.Env); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	log := logger.L()
	log.Info("PlayerLedger Backend initializing",
		zap.String("env", cfg.App.Env),
		zap.Int("port", cfg.Server.Port),
	)

	// ═══════════════════════════════════════════════════════
	// 3. 連接 Database（§6）
	// ═══════════════════════════════════════════════════════
	db, err := database.Connect(cfg.Database)
	if err != nil {
		log.Fatal("failed to connect database", zap.Error(err))
	}
	log.Info("database connected")

	// ═══════════════════════════════════════════════════════
	// 4. 執行 Migrations（§13）
	// ═══════════════════════════════════════════════════════
	if err := database.RunMigrations(cfg.Database); err != nil {
		log.Fatal("failed to run migrations", zap.Error(err))
	}
	log.Info("migrations completed")

	// ═══════════════════════════════════════════════════════
	// 5. 連接 Redis（§7）
	// ═══════════════════════════════════════════════════════
	redisClient, err := redis.Connect(cfg.Redis)
	if err != nil {
		log.Fatal("failed to connect redis", zap.Error(err))
	}
	log.Info("redis connected")

	// ═══════════════════════════════════════════════════════
	// 6. 初始化 Audit Logger（§18.3）
	// ═══════════════════════════════════════════════════════
	auditLogger, err := audit.NewZapLogger(cfg.Log.AuditPath)
	if err != nil {
		log.Fatal("failed to init audit logger", zap.Error(err))
	}
	auditSink := "stdout"
	if cfg.Log.AuditPath != "" {
		auditSink = cfg.Log.AuditPath
	}
	log.Info("audit logger initialized", zap.String("sink", auditSink))

	// ═══════════════════════════════════════════════════════
	// 7. 初始化 JWT Manager & FamilyStore（§7.4 / §8）
	// ═══════════════════════════════════════════════════════
	jwtManager := jwt.NewManager(cfg.JWT)
	preloadCtx, preloadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	familyStore, err := redis.NewFamilyStore(preloadCtx, redisClient, cfg.JWT)
	preloadCancel()
	if err != nil {
		log.Fatal("failed to initialize family store (lua script preload)", zap.Error(err))
	}
	log.Info("jwt & family store initialized")

	// ═══════════════════════════════════════════════════════
	// 8. 初始化 Metrics（§18.2）
	// ═══════════════════════════════════════════════════════
	sqlDB, err := db.DB()
	if err != nil {
		log.Warn("failed to get sql.DB for metrics", zap.Error(err))
	} else {
		version := os.Getenv("VERSION")
		if version == "" {
			version = "dev"
		}
		commit := os.Getenv("COMMIT")
		if commit == "" {
			commit = "unknown"
		}
		metrics.Init(sqlDB, version, commit)
		log.Info("metrics initialized")
	}

	// ═══════════════════════════════════════════════════════
	// 9. 初始化 Rate Limiter Store（§15.4）
	// ═══════════════════════════════════════════════════════
	var limiterStore limiter.Store
	if cfg.RateLimit.Enabled {
		limiterStore, err = ratelimit.NewRedisStore(redisClient, "ratelimit")
		if err != nil {
			log.Warn("failed to init rate limiter store (will fail-open)", zap.Error(err))
			limiterStore = nil
		} else {
			log.Info("rate limiter store initialized")
		}
	}

	// ═══════════════════════════════════════════════════════
	// 10. 初始化 Repositories、Hasher、Blacklist、AuthService
	// ═══════════════════════════════════════════════════════
	cmsUserRepo := repository.NewCMSUserRepository(db)
	memberRepo := repository.NewMemberRepository(db)
	depositRepo := repository.NewDepositRecordRepository(db)
	bcryptHasher := hasher.NewBcryptHasher(cfg.JWT.BcryptCost)
	blacklist := redis.NewAccessTokenBlacklist(redisClient)
	userRevoke := redis.NewUserRevocationStore(redisClient)

	// Seed super admin（取代規格 §13.5 原 SQL migration；改由 ADMIN_USERNAME/ADMIN_PASSWORD env 注入）
	created, err := service.EnsureAdminFromConfig(
		context.Background(),
		cmsUserRepo, bcryptHasher,
		cfg.Admin.Username, cfg.Admin.Password,
	)
	if err != nil {
		log.Fatal("seed admin failed", zap.Error(err))
	}
	if created {
		log.Info("super admin created", zap.String("username", cfg.Admin.Username))
	} else if cfg.Admin.Username != "" {
		log.Info("super admin already exists, skipping seed", zap.String("username", cfg.Admin.Username))
	}

	authService := service.NewAuthService(
		cmsUserRepo, memberRepo, jwtManager, bcryptHasher,
		familyStore, blacklist, auditLogger,
		cfg.JWT.AccessTTL,   // 固定 access token TTL（§8.2）
		cfg.JWT.GraceWindow, // Refresh rotation 重送容忍窗（§8.2.1）
	)
	log.Info("auth service initialized")

	depositService := service.NewDepositService(depositRepo, memberRepo, auditLogger)
	log.Info("deposit service initialized")

	playerService := service.NewPlayerService(memberRepo, auditLogger)
	log.Info("player service initialized")

	// CMS user 管理服務（cms-users-api §9）。
	// userRevocationTTL = max(ClientPolicies.AbsoluteTTL) + 24h 安全餘量（§4.3）。
	var maxAbsTTL time.Duration
	for _, p := range cfg.JWT.ClientPolicies {
		if p.AbsoluteTTL > maxAbsTTL {
			maxAbsTTL = p.AbsoluteTTL
		}
	}
	userRevocationTTL := maxAbsTTL + 24*time.Hour
	transactor := repository.NewTransactor(db)
	cmsUserService := service.NewCMSUserService(
		cmsUserRepo, transactor, bcryptHasher,
		familyStore, userRevoke, userRevocationTTL, auditLogger,
	)
	log.Info("cms user service initialized", zap.Duration("user_revocation_ttl", userRevocationTTL))

	// ═══════════════════════════════════════════════════════
	// 11. 建立 Gin Router & 中介層（§9.2）
	// 順序：RequestID → GinRecovery → GinLogger → SecureHeaders → CORS → MaxBodyBytes → Metrics
	// ═══════════════════════════════════════════════════════
	gin.SetMode(cfg.Server.GinMode)
	router := gin.New()

	// 永遠呼叫 SetTrustedProxies；空 slice = 完全不信任 proxy header（§9.2 安全）
	if err := router.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Warn("failed to set trusted proxies", zap.Error(err))
	}

	router.Use(
		logger.RequestID(),
		httpx.GinRecovery(),
		logger.GinLogger("/health", "/health/ready", cfg.Metrics.Path),
		httpx.SecureHeaders(cfg.App.Env),
		cors.New(cors.Config{
			AllowOrigins:     cfg.Server.AllowedOrigins,
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Accept-Language", "Authorization", "X-Request-ID"},
			ExposeHeaders:    []string{"X-Request-ID", "Retry-After"},
			AllowCredentials: cfg.Server.AllowCredentials,
			MaxAge:           12 * time.Hour,
		}),
		httpx.MaxBodyBytes(cfg.Server.MaxRequestBody),
		metrics.GinMiddleware(),
	)

	// ═══════════════════════════════════════════════════════
	// 12. 路由注冊
	// ═══════════════════════════════════════════════════════

	// 健康檢查（不受 auth / rate limit 保護，§11 / §11.3）
	healthHandler := handler.NewHealthHandlerWithRedis(db, redisClient, familyStore.ScriptsLoaded)
	router.GET("/health", healthHandler.Live)
	router.GET("/health/ready", healthHandler.Ready)

	// Metrics（由 k8s NetworkPolicy 網路層隔離，§18.1）
	router.GET(cfg.Metrics.Path, metrics.Handler())

	// /api - IP 層限流（§15.2）。CMS 為內部工具不需版本隔離，auth / member 端點亦不做版本控制，統一無版本前綴。
	apiGroup := router.Group("/api")
	if limiterStore != nil && cfg.RateLimit.Enabled {
		apiGroup.Use(ratelimit.IPMiddleware(cfg.RateLimit.IPPeriod, cfg.RateLimit.IPLimit, limiterStore))
	}

	// Auth endpoints
	authHandler := handler.NewAuthHandler(authService)
	authGroup := apiGroup.Group("/auth")
	{
		authGroup.POST("/register", authHandler.Register)
		authGroup.POST("/login", authHandler.Login)
		authGroup.POST("/refresh", authHandler.Refresh)

		// 需要 auth 的 endpoints — AuthMiddleware + User 層限流
		authGroupAuth := authGroup.Group("").Use(jwt.AuthMiddleware(jwtManager, blacklist, userRevoke))
		if limiterStore != nil && cfg.RateLimit.Enabled {
			authGroupAuth.Use(ratelimit.UserMiddleware(cfg.RateLimit.UserPeriod, cfg.RateLimit.UserLimit, limiterStore))
		}
		{
			authGroupAuth.POST("/logout", authHandler.Logout)
			authGroupAuth.GET("/sessions", authHandler.ListSessions)
			authGroupAuth.DELETE("/sessions/:fid", authHandler.RevokeSession)
			authGroupAuth.POST("/sessions/revoke-all", authHandler.RevokeAllSessions)
		}
	}

	// Member deposit endpoints（/api/me/deposit-records）
	depositHandler := handler.NewDepositHandler(depositService)
	memberDepositGroup := apiGroup.Group("/me").
		Use(jwt.AuthMiddleware(jwtManager, blacklist, userRevoke)).
		Use(jwt.RequireUserType(jwt.UserTypeMember))
	{
		memberDepositGroup.GET("/deposit-records", depositHandler.ListMine)
	}

	// CMS endpoints（/api/cms/*）— AuthMiddleware + RequireUserType(cms) + per-route RequireRole（§2 / §3）
	//   POST   建立 → admin, user
	//   GET     查詢 → 全 CMS staff（admin, user, viewer）
	//   PATCH  改狀態/備註 → admin only
	cmsGroup := router.Group("/api/cms").
		Use(jwt.AuthMiddleware(jwtManager, blacklist, userRevoke)).
		Use(jwt.RequireUserType(jwt.UserTypeCMS))
	cmsUserHandler := handler.NewCMSUserHandler(cmsUserService)
	playerHandler := handler.NewPlayerHandler(playerService)
	{
		cmsGroup.POST("/deposit-records", jwt.RequireRole(jwt.RoleAdmin, jwt.RoleUser), depositHandler.Create)
		cmsGroup.GET("/deposit-records", depositHandler.List)
		cmsGroup.GET("/deposit-records/:id", depositHandler.Get)
		cmsGroup.PATCH("/deposit-records/:id", jwt.RequireRole(jwt.RoleAdmin), depositHandler.UpdateStatus)

		// Players（players-api §3）。唯讀，全 CMS staff 可查；viewer 的 email/phone 由 handler 遮罩。
		cmsGroup.GET("/players", playerHandler.Search)
		cmsGroup.GET("/players/:id", playerHandler.Get)

		// CMS Users（cms-users-api §3）。/me 必須先於 /:id 註冊（§14）。
		//   GET     讀 → 全 CMS staff；PATCH/DELETE :id → admin only；PATCH /me → 自己
		cmsGroup.PATCH("/users/me", cmsUserHandler.UpdateSelf)
		cmsGroup.GET("/users", cmsUserHandler.List)
		cmsGroup.GET("/users/:id", cmsUserHandler.Get)
		cmsGroup.PATCH("/users/:id", jwt.RequireRole(jwt.RoleAdmin), cmsUserHandler.Update)
		cmsGroup.DELETE("/users/:id", jwt.RequireRole(jwt.RoleAdmin), cmsUserHandler.Delete)
	}

	// ═══════════════════════════════════════════════════════
	// 13. 啟動 HTTP Server（§14）
	// ═══════════════════════════════════════════════════════
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	go func() {
		log.Info("HTTP server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP server error", zap.Error(err))
		}
	}()

	log.Info("PlayerLedger Backend started")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info("Shutting down server")

	// ═══════════════════════════════════════════════════════
	// Graceful Shutdown 順序（§14.2）：HTTP → DB → Redis → Audit → App logger
	// ═══════════════════════════════════════════════════════
	shutCtx, shutCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutCancel()

	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("HTTP server shutdown error", zap.Error(err))
	}

	if err := database.Close(db); err != nil {
		log.Error("database close error", zap.Error(err))
	}

	if err := redis.Close(redisClient); err != nil {
		log.Error("redis close error", zap.Error(err))
	}

	if err := auditLogger.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "audit logger sync error: %v\n", err)
	}

	// zap.Sync 對 stderr / stdout 在多數平台會回 syscall error（已知 quirk）；
	// shutdown path 上沒地方再 log warn 也無意義，明確忽略並標記 gosec。
	_ = logger.Sync() // #nosec G104 -- zap Sync on console sink is best-effort
	log.Info("Server stopped")
}
