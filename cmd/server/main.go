package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/handler"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/logger"
)

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	if err := logger.Init(cfg.Log.Format, cfg.Log.Level, cfg.Log.Service, cfg.App.Env); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}

	log := logger.L()
	log.Info("PlayerLedger Backend started")

	// 设置 Gin 模式
	gin.SetMode(cfg.Server.GinMode)

	// 创建路由引擎
	router := gin.New()

	// 应用中间件（按顺序）
	router.Use(logger.RequestID())
	router.Use(httpx.Recovery())
	router.Use(logger.GinLogger("/health", "/health/ready", "/metrics"))
	router.Use(httpx.SecureHeaders(cfg.App.Env))
	router.Use(httpx.BodyLimit(cfg.Server.MaxRequestBody))

	// 设置 TrustedProxies
	if len(cfg.Server.TrustedProxies) > 0 {
		router.SetTrustedProxies(cfg.Server.TrustedProxies)
	}

	// 健康检查
	healthHandler := handler.NewHealthHandler()
	router.GET("/health", healthHandler.GetHealth)
	router.GET("/health/ready", healthHandler.GetReadiness)

	// TODO: 连接数据库、Redis、初始化其他 handlers

	// 启动 HTTP 服务器
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

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info("Shutting down server")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error(fmt.Sprintf("Server shutdown error: %v", err))
	}

	// 同步日志
	logger.Sync()

	log.Info("Server stopped")
}
