package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthHandler 健康检查
type HealthHandler struct {
}

// NewHealthHandler 创建健康检查 handler
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// GetHealth 返回简单的健康状态
// GET /health
func (h *HealthHandler) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// GetReadiness 返回就绪状态（检查依赖服务）
// GET /health/ready
func (h *HealthHandler) GetReadiness(c *gin.Context) {
	// TODO: 检查 DB、Redis、FamilyStore.ScriptsLoaded 等
	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}
