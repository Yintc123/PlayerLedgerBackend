package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// readyCheckTimeout 為就緒探測逐一檢查依賴的整體上限（§11.3）。
const readyCheckTimeout = 2 * time.Second

// HealthHandler 健康檢查。
//
// 依賴以「ping 函式」形式注入，而非持有具體 client：
//   - production 由建構子接上 *gorm.DB / *redis.Client
//   - 測試以 fake 函式替換，無需真實 PG / Redis（符合本專案 fake 而非 mock 的原則）
//
// 任一 ping 函式為 nil 代表該元件未設定，視為健康並回報 "ok"。
type HealthHandler struct {
	pingDB      func(ctx context.Context) error
	pingRedis   func(ctx context.Context) error
	familyReady func() bool
}

// NewHealthHandler 建立不含 Redis 的健康檢查 handler（僅 /health）。
func NewHealthHandler(db *gorm.DB) *HealthHandler {
	return &HealthHandler{pingDB: gormPinger(db)}
}

// NewHealthHandlerWithRedis 建立含 Redis + FamilyStore 的健康檢查 handler。
func NewHealthHandlerWithRedis(db *gorm.DB, r *redis.Client, familyReady func() bool) *HealthHandler {
	return &HealthHandler{
		pingDB:      gormPinger(db),
		pingRedis:   redisPinger(r),
		familyReady: familyReady,
	}
}

// gormPinger 將 *gorm.DB 包成 ping 函式；db 為 nil 時回 nil（視為未設定）。
func gormPinger(db *gorm.DB) func(ctx context.Context) error {
	if db == nil {
		return nil
	}
	return func(ctx context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	}
}

// redisPinger 將 *redis.Client 包成 ping 函式；client 為 nil 時回 nil（視為未設定）。
func redisPinger(r *redis.Client) func(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return func(ctx context.Context) error {
		return r.Ping(ctx).Err()
	}
}

// Live GET /health — 簡單存活探測，僅確認 process 能回應（§11.3）。
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready GET /health/ready — 就緒探測，逐一檢查所有依賴並回傳各元件狀態（§11.3）。
// 任一元件不健康回 503；response body 永遠包含所有元件狀態便於診斷。
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readyCheckTimeout)
	defer cancel()

	status := gin.H{
		"database":             "ok",
		"redis":                "ok",
		"family_store_scripts": "ok",
	}
	healthy := true

	// 檢查 database（PG）
	if h.pingDB != nil && h.pingDB(ctx) != nil {
		status["database"] = "unhealthy"
		healthy = false
	}

	// 檢查 Redis（若已設定）
	if h.pingRedis != nil && h.pingRedis(ctx) != nil {
		status["redis"] = "unhealthy"
		healthy = false
	}

	// 檢查 FamilyStore Lua 腳本（若已設定）
	if h.familyReady != nil && !h.familyReady() {
		status["family_store_scripts"] = "unhealthy"
		healthy = false
	}

	if !healthy {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": status})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status})
}
