package httpx

import (
	"github.com/gin-gonic/gin"
	"github.com/unrolled/secure"
)

// SecureHeaders 注入安全 HTTP header（§9.3）。
// 使用 unrolled/secure 套件（規格 §1.2）統一處理；HSTS 僅 staging / prod
// 啟用（本機開發無 TLS 不應設定）。
func SecureHeaders(env string) gin.HandlerFunc {
	isProd := env == "staging" || env == "prod"

	sec := secure.New(secure.Options{
		ContentTypeNosniff:    true,
		FrameDeny:             true,
		BrowserXssFilter:      true,
		ContentSecurityPolicy: "default-src 'none'; frame-ancestors 'none'",
		PermissionsPolicy:     "camera=(), microphone=(), geolocation=(), payment=(), fullscreen=()",
		ReferrerPolicy:        "no-referrer",
		// HSTS
		STSSeconds:           ternaryInt64(isProd, 31536000, 0),
		STSIncludeSubdomains: isProd,
		IsDevelopment:        !isProd, // dev/test 不發 HSTS
	})

	return func(c *gin.Context) {
		if err := sec.Process(c.Writer, c.Request); err != nil {
			c.AbortWithStatus(400)
			return
		}
		c.Next()
	}
}

func ternaryInt64(cond bool, t, f int64) int64 {
	if cond {
		return t
	}
	return f
}
