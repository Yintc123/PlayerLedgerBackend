package handler

import (
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
