package httpx

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// BodyLimit 限制请求体大小
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = io.NopCloser(io.LimitReader(c.Request.Body, maxBytes))
		c.Next()
	}
}

// HandleBodyTooLarge 处理请求体过大的错误
func HandleBodyTooLarge(c *gin.Context) {
	WriteError(c, http.StatusRequestEntityTooLarge, "request_body_too_large")
}
