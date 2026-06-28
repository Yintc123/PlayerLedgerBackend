package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// HealthHandler 健康检查
type HealthHandler struct {
	db         *gorm.DB
	redisOnce  *redis.Client
	familyReady func() bool
}

// NewHealthHandler 创建健康检查 handler
func NewHealthHandler(db *gorm.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// NewHealthHandlerWithRedis 创建含 Redis + FamilyStore 的健康检查 handler
func NewHealthHandlerWithRedis(db *gorm.DB, redis *redis.Client, familyReady func() bool) *HealthHandler {
	return &HealthHandler{
		db:         db,
		redisOnce:  redis,
		familyReady: familyReady,
	}
}

// GetHealth 返回简单的健康状态
// GET /health
func (h *HealthHandler) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// GetReadiness 返回就绪状态（检查依赖服务，§11.3）
// GET /health/ready
func (h *HealthHandler) GetReadiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// 检查数据库
	sqlDB, err := h.db.DB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"reason": "database unavailable",
		})
		return
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"reason": "database ping failed",
		})
		return
	}

	// 检查 Redis（可选，仅在配置时）
	if h.redisOnce != nil {
		if err := h.redisOnce.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "not_ready",
				"reason": "redis unavailable",
			})
			return
		}
	}

	// 检查 FamilyStore Lua 脚本（可选，§7.4）
	if h.familyReady != nil && !h.familyReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"reason": "family store scripts not loaded",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}
