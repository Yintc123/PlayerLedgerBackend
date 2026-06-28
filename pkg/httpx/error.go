package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/pkg/logger"
	"go.uber.org/zap"
)

// ErrorResponse 错误响应格式（避免循环导入，这里重新定义）
type ErrorResponse struct {
	Success   bool                `json:"success"`
	RequestID string              `json:"request_id"`
	Error     string              `json:"error"`
	Details   []map[string]string `json:"details,omitempty"`
}

// FieldError 字段验证错误
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// WriteError 统一的错误响应写入函数
func WriteError(c *gin.Context, statusCode int, errorCode string) {
	requestID := logger.GetRequestID(c)
	resp := ErrorResponse{
		Success:   false,
		RequestID: requestID,
		Error:     errorCode,
	}

	c.JSON(statusCode, resp)
}

// HandleError 根据错误类型生成 HTTP 响应
// 错误映射遵循 §12.4 规范：各端点可能的 error 字串对应
func HandleError(c *gin.Context, err error) {
	requestID := logger.GetRequestID(c)
	statusCode := http.StatusInternalServerError
	errorCode := "internal_server_error"

	// 处理 ShouldBindJSON 错误 — validation failure（§12.3）
	// gin.Context.ShouldBindJSON 返回多种错误类型（JSON 语法错、字段绑定失败、struct tag 验证失败）
	if err.Error() == "EOF" || strings.Contains(err.Error(), "json") || strings.Contains(err.Error(), "binding") {
		statusCode = http.StatusBadRequest
		errorCode = "invalid_input"
		WriteErrorWithDetails(c, statusCode, errorCode, nil)
		return
	}

	// 检查是否为 *apperr.AppError
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		statusCode, errorCode = mapAppErrorToHTTP(appErr.Code)
		WriteErrorWithDetails(c, statusCode, errorCode, nil)
		return
	}

	// 检查是否为 sentinel error
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		statusCode = http.StatusNotFound
		errorCode = "resource_not_found"
	case errors.Is(err, apperr.ErrUnauthorized):
		statusCode = http.StatusUnauthorized
		errorCode = "unauthorized"
	case errors.Is(err, apperr.ErrForbidden):
		statusCode = http.StatusForbidden
		errorCode = "forbidden"
	case errors.Is(err, apperr.ErrConflict):
		statusCode = http.StatusConflict
		errorCode = "username_taken"
	case errors.Is(err, apperr.ErrInvalidInput):
		statusCode = http.StatusBadRequest
		errorCode = "invalid_input"
	case errors.Is(err, apperr.ErrTokenExpired):
		statusCode = http.StatusUnauthorized
		errorCode = "token_expired"
	case errors.Is(err, apperr.ErrAbsoluteExpired):
		statusCode = http.StatusUnauthorized
		errorCode = "absolute_expired"
	case errors.Is(err, apperr.ErrInvalidToken):
		statusCode = http.StatusUnauthorized
		errorCode = "invalid_token"
	case errors.Is(err, apperr.ErrReplayDetected):
		statusCode = http.StatusUnauthorized
		errorCode = "replay_detected"
	case errors.Is(err, apperr.ErrSessionNotFound):
		statusCode = http.StatusUnauthorized
		errorCode = "session_not_found"
	case errors.Is(err, apperr.ErrSessionRevoked):
		statusCode = http.StatusUnauthorized
		errorCode = "session_revoked"
	case errors.Is(err, apperr.ErrUsernameTaken):
		statusCode = http.StatusConflict
		errorCode = "username_taken"
	case errors.Is(err, apperr.ErrWeakPassword):
		statusCode = http.StatusUnprocessableEntity
		errorCode = "weak_password"
	case errors.Is(err, apperr.ErrInvalidClient):
		statusCode = http.StatusBadRequest
		errorCode = "invalid_client"
	case errors.Is(err, apperr.ErrTooManyRequests):
		statusCode = http.StatusTooManyRequests
		errorCode = "too_many_requests"
	default:
		logger.L().Error("unhandled error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
		statusCode = http.StatusInternalServerError
		errorCode = "internal_server_error"
	}

	WriteErrorWithDetails(c, statusCode, errorCode, nil)
}

// WriteErrorWithDetails 带详情的错误响应
func WriteErrorWithDetails(c *gin.Context, statusCode int, errorCode string, details []FieldError) {
	requestID := logger.GetRequestID(c)
	resp := ErrorResponse{
		Success:   false,
		RequestID: requestID,
		Error:     errorCode,
	}

	// 转换 FieldError 为 map[string]string
	if len(details) > 0 {
		resp.Details = make([]map[string]string, len(details))
		for i, fe := range details {
			resp.Details[i] = map[string]string{
				"field":   fe.Field,
				"message": fe.Message,
			}
		}
	}

	c.JSON(statusCode, resp)
}

// mapAppErrorToHTTP 将 AppError.Code 映射到 HTTP status + error code
func mapAppErrorToHTTP(code string) (int, string) {
	switch code {
	case "forbidden":
		return http.StatusForbidden, "forbidden"
	case "use_logout_instead":
		return http.StatusForbidden, "forbidden"
	default:
		return http.StatusInternalServerError, "internal_server_error"
	}
}
