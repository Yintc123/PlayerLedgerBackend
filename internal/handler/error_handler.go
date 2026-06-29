package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/logger"
	"go.uber.org/zap"
)

// HandleError 將 domain error 轉為 HTTP 回應（§12.3 / §12.4）。
// 呼叫端不需再處理 errorCode 映射；一律由此統一轉換。
func HandleError(c *gin.Context, err error) {
	// 1) Struct tag 驗證失敗（go-playground/validator）— 帶 details 回 400（§12.3）。
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		details := make([]httpx.FieldError, 0, len(ve))
		for _, fe := range ve {
			details = append(details, httpx.FieldError{
				Field:   fe.Field(),
				Message: fmt.Sprintf("failed on the '%s' tag", fe.Tag()),
			})
		}
		httpx.WriteErrorWithDetails(c, http.StatusBadRequest, "invalid input", details)
		return
	}

	// 2) ShouldBindJSON 失敗 — JSON 解析錯誤或 EOF（§12.3）
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// 3) Gin 包裝後的 EOF（型別差異）
	if err.Error() == "EOF" {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// AppError（明確錯誤碼）
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		statusCode, errorCode := mapAppErrorToHTTP(appErr.Code)
		httpx.WriteError(c, statusCode, errorCode)
		return
	}

	// Sentinel errors（§12.4 error 字串對照表）
	switch {
	case errors.Is(err, apperr.ErrInvalidInput):
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
	case errors.Is(err, apperr.ErrInvalidClient):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_client")
	case errors.Is(err, apperr.ErrUsernameTaken):
		httpx.WriteError(c, http.StatusConflict, "username_taken")
	case errors.Is(err, apperr.ErrConflict):
		httpx.WriteError(c, http.StatusConflict, "resource already exists")
	case errors.Is(err, apperr.ErrWeakPassword):
		httpx.WriteError(c, http.StatusUnprocessableEntity, "weak_password")
	case errors.Is(err, apperr.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "resource not found")
	case errors.Is(err, apperr.ErrUnauthorized):
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, apperr.ErrForbidden):
		httpx.WriteError(c, http.StatusForbidden, "forbidden")
	case errors.Is(err, apperr.ErrUseLogoutInstead):
		httpx.WriteError(c, http.StatusBadRequest, "use_logout_instead")
	case errors.Is(err, apperr.ErrTokenExpired):
		httpx.WriteError(c, http.StatusUnauthorized, "token_expired")
	case errors.Is(err, apperr.ErrAbsoluteExpired):
		httpx.WriteError(c, http.StatusUnauthorized, "absolute_expired")
	case errors.Is(err, apperr.ErrInvalidToken):
		httpx.WriteError(c, http.StatusUnauthorized, "invalid_token")
	case errors.Is(err, apperr.ErrReplayDetected):
		httpx.WriteError(c, http.StatusUnauthorized, "replay_detected")
	case errors.Is(err, apperr.ErrFamilyNotFound):
		httpx.WriteError(c, http.StatusUnauthorized, "session_not_found")
	case errors.Is(err, apperr.ErrSessionRevoked):
		httpx.WriteError(c, http.StatusUnauthorized, "session_revoked")
	case errors.Is(err, apperr.ErrTooManyRequests):
		httpx.WriteError(c, http.StatusTooManyRequests, "too many requests")

	// deposit-records-api sentinels
	case errors.Is(err, apperr.ErrInvalidTransition):
		httpx.WriteError(c, http.StatusUnprocessableEntity, "invalid_transition")
	case errors.Is(err, apperr.ErrReferenceNoConflict):
		httpx.WriteError(c, http.StatusConflict, "resource already exists")

	// cms-users-api §8 sentinels
	case errors.Is(err, apperr.ErrLastAdminLockout):
		httpx.WriteError(c, http.StatusUnprocessableEntity, "last_admin_lockout")
	case errors.Is(err, apperr.ErrCannotDeleteSelf):
		httpx.WriteError(c, http.StatusUnprocessableEntity, "cannot_delete_self")
	case errors.Is(err, apperr.ErrCannotChangeOwnRole):
		httpx.WriteError(c, http.StatusUnprocessableEntity, "cannot_change_own_role")
	case errors.Is(err, apperr.ErrCurrentPasswordMismatch):
		httpx.WriteError(c, http.StatusUnauthorized, "current_password_mismatch")

	default:
		requestID := logger.GetRequestID(c)
		logger.L().Error("unhandled error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
		httpx.WriteError(c, http.StatusInternalServerError, "internal server error")
	}
}

// mapAppErrorToHTTP 將 AppError.Code 映射到 HTTP status + error code。
func mapAppErrorToHTTP(code string) (int, string) {
	switch code {
	case "forbidden":
		return http.StatusForbidden, "forbidden"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}
