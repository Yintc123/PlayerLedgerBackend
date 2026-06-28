package httpx

import (
	"github.com/gin-gonic/gin"
)

// SecureHeaders 添加安全 HTTP 头
func SecureHeaders(env string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// X-Content-Type-Options
		c.Header("X-Content-Type-Options", "nosniff")

		// X-Frame-Options
		c.Header("X-Frame-Options", "DENY")

		// X-XSS-Protection
		c.Header("X-XSS-Protection", "1; mode=block")

		// HSTS 仅在 staging/prod 启用
		if env == "staging" || env == "prod" {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		c.Next()
	}
}
