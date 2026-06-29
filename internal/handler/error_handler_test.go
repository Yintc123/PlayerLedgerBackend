package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yintengching/playerledger/internal/apperr"
)

// errorMapCase 描述一次 sentinel → (status, error code) 的映射預期。
type errorMapCase struct {
	name       string
	err        error
	wantStatus int
	wantCode   string
}

// runHandleErrorCases 跑一組 sentinel → HTTP 對照測試，回 body 為標準 ErrorResponse。
func runHandleErrorCases(t *testing.T, cases []errorMapCase) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			HandleError(ctx, c.err)

			assert.Equal(t, c.wantStatus, w.Code)

			var resp struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.False(t, resp.Success)
			assert.Equal(t, c.wantCode, resp.Error)
		})
	}
}

// TestHandleError_CMSUserSentinels 驗證 cms-users-api §8 4 個新 sentinel 的 HTTP 映射
func TestHandleError_CMSUserSentinels(t *testing.T) {
	runHandleErrorCases(t, []errorMapCase{
		{"ErrLastAdminLockout", apperr.ErrLastAdminLockout, http.StatusUnprocessableEntity, "last_admin_lockout"},
		{"ErrCannotDeleteSelf", apperr.ErrCannotDeleteSelf, http.StatusUnprocessableEntity, "cannot_delete_self"},
		{"ErrCannotChangeOwnRole", apperr.ErrCannotChangeOwnRole, http.StatusUnprocessableEntity, "cannot_change_own_role"},
		{"ErrCurrentPasswordMismatch", apperr.ErrCurrentPasswordMismatch, http.StatusUnauthorized, "current_password_mismatch"},
	})
}

// TestHandleError_ValidationDetailsUseJSONFieldName 驗證 validation 錯誤的 details[].field
// 使用 JSON 欄位名（snake_case），對齊 OpenAPI ErrorResponse.details 慣例（非 Go struct 名）。
func TestHandleError_ValidationDetailsUseJSONFieldName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type body struct {
		ClientID string `json:"client_id" binding:"required"`
	}

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	var b body
	err := ctx.ShouldBindJSON(&b)
	require.Error(t, err)
	HandleError(ctx, err)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp struct {
		Details []struct {
			Field string `json:"field"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Details)
	assert.Equal(t, "client_id", resp.Details[0].Field)
}

// TestHandleError_UseLogoutInstead 驗證撤銷自己當前 family 的 sentinel 映射為
// 400 use_logout_instead（對齊 OpenAPI / ADR 007，取代舊的 403 forbidden）。
func TestHandleError_UseLogoutInstead(t *testing.T) {
	runHandleErrorCases(t, []errorMapCase{
		{"ErrUseLogoutInstead", apperr.ErrUseLogoutInstead, http.StatusBadRequest, "use_logout_instead"},
	})
}
