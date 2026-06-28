package httpx

import (
	"github.com/gin-gonic/gin"
)

// SecureHeaders 注入安全 HTTP header（§9.3）。
// HSTS 僅 staging / prod 啟用（本機開發無 TLS 不應設定）。
func SecureHeaders(env string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), fullscreen=()")
		c.Header("Referrer-Policy", "no-referrer")

		if env == "staging" || env == "prod" {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		c.Next()
	}
}
