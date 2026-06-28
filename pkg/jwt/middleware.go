package jwt

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/httpx"
)

// AuthMiddleware JWT 认证中间件。
func AuthMiddleware(jwtManager Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		token := parts[1]
		claims, err := jwtManager.VerifyAccess(token)
		if err != nil {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		SetClaims(c, claims)
		c.Next()
	}
}
