package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/logger"
	"go.uber.org/zap"
)

// GinRecovery 捕獲 panic 並回 500（§9.3）。
// 使用 gin.CustomRecovery 取得 recovered value；記錄 stack trace。
func GinRecovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.L().Error("panic recovered",
			zap.Any("panic", recovered),
			zap.String("request_id", logger.GetRequestID(c)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Stack("stack"),
		)
		WriteError(c, http.StatusInternalServerError, "internal server error")
	})
}
