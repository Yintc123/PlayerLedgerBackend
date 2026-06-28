package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/logger"
)

// WriteError 統一的錯誤回應寫入函式（§12.4）。
// 使用 AbortWithStatusJSON 以確保後續 middleware 不再執行。
func WriteError(c *gin.Context, statusCode int, errorCode string) {
	requestID := logger.GetRequestID(c)
	c.AbortWithStatusJSON(statusCode, gin.H{
		"success":    false,
		"request_id": requestID,
		"error":      errorCode,
	})
}

// WriteErrorWithDetails 帶 validation 細節的錯誤回應（§12.4）。
func WriteErrorWithDetails(c *gin.Context, statusCode int, errorCode string, details []FieldError) {
	requestID := logger.GetRequestID(c)
	resp := map[string]any{
		"success":    false,
		"request_id": requestID,
		"error":      errorCode,
	}
	if len(details) > 0 {
		resp["details"] = details
	}
	c.AbortWithStatusJSON(statusCode, resp)
}

// FieldError 欄位驗證錯誤（§3.5.1 FieldError schema）。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// StatusOK 回傳 204 No Content。
func StatusNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
