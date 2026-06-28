package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes 限制 request body 大小（§9.3 / §9.5）。
// 超過限制時 http.MaxBytesReader 回傳錯誤，Gin 會呼叫 GinRecovery → 413。
func MaxBodyBytes(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
