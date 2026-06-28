package logger

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// GinLogger 记录每个 request 的访问日志
func GinLogger(skipPaths ...string) gin.HandlerFunc {
	skipMap := make(map[string]bool)
	for _, p := range skipPaths {
		skipMap[p] = true
	}

	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		// 检查是否应该跳过
		if skipMap[c.Request.URL.Path] {
			return
		}

		latency := time.Since(start).Milliseconds()
		statusCode := c.Writer.Status()

		// 确定日志级别
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

		// 只在有错误时添加 errors 字段
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("errors", c.Errors.ByType(gin.ErrorTypePrivate).String()))
		}

		L().Log(level, "http_request", fields...)
	}
}
