package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// HealthHandler 健康檢查
type HealthHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
	familyReady func() bool
}

// NewHealthHandler 建立不含 Redis 的健康檢查 handler（僅 /health）
func NewHealthHandler(db *gorm.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// NewHealthHandlerWithRedis 建立含 Redis + FamilyStore 的健康檢查 handler
func NewHealthHandlerWithRedis(db *gorm.DB, r *redis.Client, familyReady func() bool) *HealthHandler {
	return &HealthHandler{
		db:          db,
		redisClient: r,
		familyReady: familyReady,
	}
}

// Live GET /health — 簡單存活探測，僅確認 process 能回應（§11.3）。
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready GET /health/ready — 就緒探測，逐一檢查所有依賴並回傳各元件狀態（§11.3）。
// 任一元件不健康回 503；response body 永遠包含所有元件狀態便於診斷。
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	status := gin.H{
		"database":              "ok",
		"redis":                 "ok",
		"family_store_scripts":  "ok",
	}
	healthy := true

	// 檢查 database
	sqlDB, err := h.db.DB()
	if err != nil || sqlDB.PingContext(ctx) != nil {
		status["database"] = "error"
		healthy = false
	}

	// 檢查 Redis（若已設定）
	if h.redisClient != nil {
		if err := h.redisClient.Ping(ctx).Err(); err != nil {
			status["redis"] = "error"
			healthy = false
		}
	}

	// 檢查 FamilyStore Lua 腳本（若已設定）
	if h.familyReady != nil && !h.familyReady() {
		status["family_store_scripts"] = "error"
		healthy = false
	}

	if !healthy {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": status})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status})
}
