package logger

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yintengching/playerledger/pkg/ctxkey"
)

const RequestIDKey = "request_id"

// RequestID middleware 为每个 request 注入唯一 ID
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(ctxkey.RequestIDHeader)
		if !isValidRequestID(id) {
			id = uuid.New().String()
		}
		c.Set(RequestIDKey, id)
		c.Request = c.Request.WithContext(ctxkey.SetRequestID(c.Request.Context(), id))
		c.Header(ctxkey.RequestIDHeader, id)
		c.Next()
	}
}

// isValidRequestID 驗證 request ID 的合法性
func isValidRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r < 0x21 || r > 0x7E {
			return false
		}
	}
	return true
}

// GetRequestID 从 gin.Context 取得 request_id
func GetRequestID(c *gin.Context) string {
	id, _ := c.Get(RequestIDKey)
	s, _ := id.(string)
	return s
}
