package jwt

import "github.com/gin-gonic/gin"

const claimsCtxKey = "jwt_claims"

// SetClaims 将 claims 注入到 gin.Context。
func SetClaims(c *gin.Context, claims *AccessClaims) {
	c.Set(claimsCtxKey, claims)
}

// GetClaims 从 gin.Context 取得 claims。
func GetClaims(c *gin.Context) (*AccessClaims, bool) {
	v, ok := c.Get(claimsCtxKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*AccessClaims)
	return claims, ok
}
