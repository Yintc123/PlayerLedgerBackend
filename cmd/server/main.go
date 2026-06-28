package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
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
	// 1. 加载配置
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志
	if err := logger.Init(cfg.Log.Format, cfg.Log.Level, cfg.Log.Service, cfg.App.Env); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}

	log := logger.L()
	log.Info("PlayerLedger Backend initializing")

	// 2b. 初始化审计日志（§18.3）
	auditLogDir := os.Getenv("AUDIT_LOG_DIR")
	if auditLogDir == "" {
		auditLogDir = "/var/log/playerledger"
	}
	if err := audit.Init(fmt.Sprintf("%s/audit.log", auditLogDir), 100, 10, 30); err != nil {
		log.Warn(fmt.Sprintf("failed to init audit logger: %v", err))
		// 不 fatal：audit logger 故障不应阻断应用启动
	}

	// 3. 连接数据库
	db, err := database.Connect(cfg.Database)
	if err != nil {
		log.Error(fmt.Sprintf("failed to connect database: %v", err))
		os.Exit(1)
	}
	defer database.Close(db)

	// 4. 连接 Redis
	redisClient, err := redis.Connect(cfg.Redis)
	if err != nil {
		log.Error(fmt.Sprintf("failed to connect redis: %v", err))
		os.Exit(1)
	}
	defer redis.Close(redisClient)

	// 5. 初始化 JWT Manager 和 FamilyStore
	jwtManager := jwt.NewManager(cfg.JWT)
	familyStore, err := redis.NewFamilyStore(context.Background(), redisClient, cfg.JWT)
	if err != nil {
		log.Error(fmt.Sprintf("failed to init family store: %v", err))
		os.Exit(1)
	}

	// 6. 初始化 Metrics（§18.2）
	sqlDB, err := db.DB()
	if err == nil {
		metrics.Init(sqlDB, os.Getenv("VERSION"), os.Getenv("COMMIT"), time.Now().Format("2006-01-02"))
	}

	// 7. 初始化 Rate Limiting Store（§15.4）
	limiterStore, err := ratelimit.NewRedisStore(redisClient, "ratelimit")
	if err != nil {
		log.Warn(fmt.Sprintf("failed to init rate limiter store: %v", err))
		// 不 fatal：rate limiting 故障应 fail-open
	}

	// 8. 初始化 Repositories 和 Services
	cmsUserRepo := repository.NewCMSUserRepository(db)
	memberRepo := repository.NewMemberRepository(db)
	bcryptHasher := hasher.NewBcryptHasher(cfg.JWT.BcryptCost)
	blacklist := redis.NewAccessTokenBlacklist(redisClient)
	authService := service.NewAuthService(cmsUserRepo, memberRepo, jwtManager, bcryptHasher, familyStore, blacklist)

	// 9. 设置 Gin 模式
	gin.SetMode(cfg.Server.GinMode)

	// 10. 创建路由引擎
	router := gin.New()

	// 应用中间件（按顺序 §9.2）
	router.Use(logger.RequestID())
	router.Use(httpx.Recovery())
	router.Use(logger.GinLogger("/health", "/health/ready", "/metrics"))
	router.Use(httpx.SecureHeaders(cfg.App.Env))
	router.Use(httpx.BodyLimit(cfg.Server.MaxRequestBody))

	// 设置 TrustedProxies
	if len(cfg.Server.TrustedProxies) > 0 {
		router.SetTrustedProxies(cfg.Server.TrustedProxies)
	}

	// 11. 路由注册

	// 健康检查（不需要 auth、不限流）
	healthHandler := handler.NewHealthHandler(db)
	router.GET("/health", healthHandler.GetHealth)
	router.GET("/health/ready", healthHandler.GetReadiness)

	// Metrics（不需要 auth、不限流，由网络层隔离，§18.1）
	router.GET("/metrics", metrics.Handler())

	// API endpoints（应用 IP 层限流，§15.2）
	apiGroup := router.Group("/api/v1")
	if limiterStore != nil {
		apiGroup.Use(ratelimit.IPMiddleware(1*time.Minute, 100, limiterStore))
	}

	// Auth endpoints
	authHandler := handler.NewAuthHandler(authService)
	authGroup := apiGroup.Group("/auth")
	{
		authGroup.POST("/register", authHandler.Register)
		authGroup.POST("/login", authHandler.Login)
		authGroup.POST("/refresh", authHandler.Refresh)

		// 需要 auth 的 endpoints（§8.5 含 blacklist 检查）
		authGroupAuth := authGroup.Group("").Use(jwt.AuthMiddleware(jwtManager, blacklist))
		if limiterStore != nil {
			authGroupAuth.Use(ratelimit.UserMiddleware(1*time.Minute, 1000, limiterStore))
		}
		{
			authGroupAuth.POST("/logout", authHandler.Logout)
			authGroupAuth.GET("/sessions", authHandler.ListSessions)
			authGroupAuth.DELETE("/sessions/:fid", authHandler.RevokeSession)
			authGroupAuth.POST("/sessions/revoke-all", authHandler.RevokeAllSessions)
		}
	}

	// 12. 启动 HTTP 服务器
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	// 在 goroutine 中启动服务器
	go func() {
		log.Info(fmt.Sprintf("HTTP server listening on %s", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(fmt.Sprintf("HTTP server error: %v", err))
		}
	}()

	log.Info("PlayerLedger Backend started")

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info("Shutting down server")

	// Graceful shutdown（§14.2）：HTTP → Redis → DB → Audit logger → App logger
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error(fmt.Sprintf("Server shutdown error: %v", err))
	}

	// 关闭 redis（在 HTTP 之后）
	if err := redis.Close(redisClient); err != nil {
		log.Error(fmt.Sprintf("Redis close error: %v", err))
	}

	// 关闭数据库
	if err := database.Close(db); err != nil {
		log.Error(fmt.Sprintf("Database close error: %v", err))
	}

	// Sync audit logger（安全相关日志优先级最高，§14.2）
	if err := audit.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "audit logger sync error: %v\n", err)
	}

	// 同步应用日志
	logger.Sync()

	log.Info("Server stopped")
}
