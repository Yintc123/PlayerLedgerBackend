package logger

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// GinLogger 記錄每個 request 的 access log（§5.3）。
// skipPaths 中的路徑（以 c.FullPath() 比對）不寫入日誌，避免健康檢查污染。
func GinLogger(skipPaths ...string) gin.HandlerFunc {
	skipMap := make(map[string]bool)
	for _, p := range skipPaths {
		skipMap[p] = true
	}

	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		// skip 比對使用 c.FullPath()（模板路徑），避免 path param 高基數問題
		if skipMap[c.FullPath()] {
			return
		}

		latency := time.Since(start).Milliseconds()
		statusCode := c.Writer.Status()

		level := zapcore.InfoLevel
		if statusCode >= 400 && statusCode < 500 {
			level = zapcore.WarnLevel
		} else if statusCode >= 500 {
			level = zapcore.ErrorLevel
		}

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Int("status", statusCode),
			zap.Int64("latency_ms", latency),
			zap.String("client_ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.Int64("bytes_in", c.Request.ContentLength),
			zap.Int("bytes_out", c.Writer.Size()),
			zap.String("request_id", GetRequestID(c)),
		}

		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("errors", c.Errors.ByType(gin.ErrorTypePrivate).String()))
		}

		L().Log(level, "http_request", fields...)
	}
}
