package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/internal/service"
	pkgjwt "github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
)

// ─── Fake CMSUserService ───────────────────────────────────────────────────────

type fakeCMSUserService struct {
	users         map[string]*model.CMSUser
	listErr       error
	getErr        error
	updateErr     error
	deleteErr     error
	updateSelfErr error
}

func newFakeCMSUserService() *fakeCMSUserService {
	return &fakeCMSUserService{users: make(map[string]*model.CMSUser)}
}

func (s *fakeCMSUserService) List(_ context.Context, opts repository.ListCMSUsersOptions) ([]model.CMSUser, int64, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	var out []model.CMSUser
	for _, u := range s.users {
		if len(opts.RoleFilter) > 0 {
			match := false
			for _, r := range opts.RoleFilter {
				if u.Role == r {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, *u)
	}
	return out, int64(len(out)), nil
}

func (s *fakeCMSUserService) Get(_ context.Context, id string, _ bool) (*model.CMSUser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	u, ok := s.users[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return u, nil
}

func (s *fakeCMSUserService) Update(_ context.Context, _, targetID string, in service.UpdateCMSUserInput) (*model.CMSUser, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	u, ok := s.users[targetID]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	if in.Username != nil {
		u.Username = *in.Username
	}
	if in.Role != nil {
		u.Role = *in.Role
	}
	return u, nil
}

func (s *fakeCMSUserService) SoftDelete(_ context.Context, _, _ string) error {
	return s.deleteErr
}

func (s *fakeCMSUserService) UpdateSelf(_ context.Context, callerID string, in service.UpdateSelfInput) (*model.CMSUser, error) {
	if s.updateSelfErr != nil {
		return nil, s.updateSelfErr
	}
	u, ok := s.users[callerID]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	if in.Username != nil {
		u.Username = *in.Username
	}
	return u, nil
}

func (s *fakeCMSUserService) seed(id, username, role string) *model.CMSUser {
	uid := uuid.MustParse(id)
	u := &model.CMSUser{
		Base:         model.Base{ID: uid, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Username:     username,
		Role:         role,
		PasswordHash: "$2a$12$secret",
	}
	s.users[id] = u
	return u
}

// ─── router 設定（對齊 main.go §14 路由註冊順序）─────────────────────────────

func setupCMSUserRouter(t *testing.T, svc service.CMSUserService, callerID string, role pkgjwt.Role) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	_ = logOnce

	claims := &pkgjwt.AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: callerID},
		UserType:         pkgjwt.UserTypeCMS,
		Role:             role,
	}

	r := gin.New()
	r.Use(logger.RequestID())
	r.Use(injectClaims(claims))

	h := NewCMSUserHandler(svc)
	g := r.Group("/api/cms/users")
	g.PATCH("/me", h.UpdateSelf) // /me 先於 /:id
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.PATCH("/:id", pkgjwt.RequireRole(pkgjwt.RoleAdmin), h.Update)
	g.DELETE("/:id", pkgjwt.RequireRole(pkgjwt.RoleAdmin), h.Delete)

	return r
}

func newCallerID() string { return uuid.New().String() }

// ─── GET /api/cms/users ────────────────────────────────────────────────────────

func TestCMSUserHandler_List_Success_Returns200(t *testing.T) {
	svc := newFakeCMSUserService()
	svc.seed(uuid.New().String(), "alice", "admin")
	svc.seed(uuid.New().String(), "bob", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/users", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp["data"].([]any), 2)
}

// GET 列表開放全 CMS staff（含 viewer）
func TestCMSUserHandler_List_Viewer_Returns200(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleViewer)
	w := doRequest(r, http.MethodGet, "/api/cms/users", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCMSUserHandler_List_PageSizeOver100_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)
	w := doRequest(r, http.MethodGet, "/api/cms/users?page_size=101", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCMSUserHandler_List_InvalidRole_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)
	w := doRequest(r, http.MethodGet, "/api/cms/users?role=superuser", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCMSUserHandler_List_InvalidSort_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)
	w := doRequest(r, http.MethodGet, "/api/cms/users?sort=password", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCMSUserHandler_List_UsernameLikeTooShort_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)
	w := doRequest(r, http.MethodGet, "/api/cms/users?username_like=a", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── GET /api/cms/users/:id ────────────────────────────────────────────────────

func TestCMSUserHandler_Get_Success_Returns200(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "admin")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/users/"+id, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, id, resp["data"].(map[string]any)["id"])
}

// §12 #9：回應絕不含 password_hash
func TestCMSUserHandler_NoPasswordHashInResponse(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "admin")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/users/"+id, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "password_hash")
	assert.NotContains(t, w.Body.String(), "secret")
}

func TestCMSUserHandler_Get_NotFound_Returns404(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)
	w := doRequest(r, http.MethodGet, "/api/cms/users/"+uuid.New().String(), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCMSUserHandler_Get_InvalidID_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)
	w := doRequest(r, http.MethodGet, "/api/cms/users/not-a-uuid", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── PATCH /api/cms/users/:id ──────────────────────────────────────────────────

func TestCMSUserHandler_Update_Success_Returns200(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{"role": "viewer"})
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "viewer", resp["data"].(map[string]any)["role"])
}

// §12 #8：非 admin PATCH → 403（RequireRole 擋下）
func TestCMSUserHandler_Update_NonAdmin_Returns403(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleUser)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{"role": "admin"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCMSUserHandler_Update_UnknownField_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{"bogus": 1})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCMSUserHandler_Update_EmptyBody_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCMSUserHandler_Update_LastAdminLockout_Returns422(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "admin")
	svc.updateErr = apperr.ErrLastAdminLockout
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{"role": "viewer"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCMSUserHandler_Update_UsernameTaken_Returns409(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	svc.updateErr = apperr.ErrUsernameTaken
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{"username": "taken"})
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestCMSUserHandler_Update_ShortUsername_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/"+id, map[string]any{"username": "ab"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── DELETE /api/cms/users/:id ─────────────────────────────────────────────────

func TestCMSUserHandler_Delete_Success_Returns204(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodDelete, "/api/cms/users/"+id, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCMSUserHandler_Delete_NonAdmin_Returns403(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.seed(id, "alice", "user")
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleViewer)

	w := doRequest(r, http.MethodDelete, "/api/cms/users/"+id, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCMSUserHandler_Delete_CannotDeleteSelf_Returns422(t *testing.T) {
	svc := newFakeCMSUserService()
	id := uuid.New().String()
	svc.deleteErr = apperr.ErrCannotDeleteSelf
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodDelete, "/api/cms/users/"+id, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCMSUserHandler_Delete_NotFound_Returns404(t *testing.T) {
	svc := newFakeCMSUserService()
	svc.deleteErr = apperr.ErrNotFound
	r := setupCMSUserRouter(t, svc, newCallerID(), pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodDelete, "/api/cms/users/"+uuid.New().String(), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ─── PATCH /api/cms/users/me ───────────────────────────────────────────────────

func TestCMSUserHandler_UpdateSelf_Success_Returns200(t *testing.T) {
	svc := newFakeCMSUserService()
	callerID := uuid.New().String()
	svc.seed(callerID, "alice", "user")
	r := setupCMSUserRouter(t, svc, callerID, pkgjwt.RoleUser)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/me", map[string]any{"username": "alice2"})
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "alice2", resp["data"].(map[string]any)["username"])
}

// INV-4：PATCH /me 帶 role 欄位被 DisallowUnknownFields 擋下 → 400
func TestCMSUserHandler_UpdateSelf_WithRole_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	callerID := uuid.New().String()
	svc.seed(callerID, "alice", "user")
	r := setupCMSUserRouter(t, svc, callerID, pkgjwt.RoleUser)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/me", map[string]any{"role": "admin"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCMSUserHandler_UpdateSelf_CurrentPasswordMismatch_Returns401(t *testing.T) {
	svc := newFakeCMSUserService()
	callerID := uuid.New().String()
	svc.seed(callerID, "alice", "user")
	svc.updateSelfErr = apperr.ErrCurrentPasswordMismatch
	r := setupCMSUserRouter(t, svc, callerID, pkgjwt.RoleUser)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/me", map[string]any{
		"current_password": "wrong",
		"new_password":     "newpw123",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCMSUserHandler_UpdateSelf_WeakPassword_Returns422(t *testing.T) {
	svc := newFakeCMSUserService()
	callerID := uuid.New().String()
	svc.seed(callerID, "alice", "user")
	svc.updateSelfErr = apperr.ErrWeakPassword
	r := setupCMSUserRouter(t, svc, callerID, pkgjwt.RoleUser)

	// 長度合法（≥8）但缺數字 → 通過 handler schema 檢查，由 service 回 422 weak_password。
	w := doRequest(r, http.MethodPatch, "/api/cms/users/me", map[string]any{
		"current_password": "oldpw123",
		"new_password":     "passwordonly",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// TestCMSUserHandler_UpdateSelf_ShortNewPassword_Returns400 — new_password < 8（OpenAPI minLength:8 schema 違規）
// → 400 invalid input，不應走到 service 的 422 weak_password。
func TestCMSUserHandler_UpdateSelf_ShortNewPassword_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	callerID := uuid.New().String()
	svc.seed(callerID, "alice", "user")
	svc.updateSelfErr = apperr.ErrWeakPassword // 即使 service 會回 422，handler 應先攔下回 400
	r := setupCMSUserRouter(t, svc, callerID, pkgjwt.RoleUser)

	w := doRequest(r, http.MethodPatch, "/api/cms/users/me", map[string]any{
		"current_password": "oldpw123",
		"new_password":     "ab1",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCMSUserHandler_UpdateSelf_TooLongNewPassword_Returns400 — new_password > 256（OpenAPI maxLength:256）→ 400。
func TestCMSUserHandler_UpdateSelf_TooLongNewPassword_Returns400(t *testing.T) {
	svc := newFakeCMSUserService()
	callerID := uuid.New().String()
	svc.seed(callerID, "alice", "user")
	r := setupCMSUserRouter(t, svc, callerID, pkgjwt.RoleUser)

	long := strings.Repeat("a1", 200) // 400 字元
	w := doRequest(r, http.MethodPatch, "/api/cms/users/me", map[string]any{
		"current_password": "oldpw123",
		"new_password":     long,
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
