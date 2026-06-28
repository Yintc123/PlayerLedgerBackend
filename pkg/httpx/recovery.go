package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/logger"
	"go.uber.org/zap"
)

// Recovery 捕获 panic 并返回错误响应
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				requestID := logger.GetRequestID(c)
				logger.L().Error("panic recovered",
					zap.Any("panic", err),
					zap.String("request_id", requestID),
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
				)

				WriteError(c, http.StatusInternalServerError, "internal_server_error")
			}
		}()

		c.Next()
	}
}
