package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
	"github.com/yintengching/playerledger/pkg/redis"
)

// ===== Fake Repositories =====

type FakeCMSUserRepository struct {
	users map[string]*model.CMSUser
}

func NewFakeCMSUserRepository() *FakeCMSUserRepository {
	return &FakeCMSUserRepository{
		users: make(map[string]*model.CMSUser),
	}
}

func (r *FakeCMSUserRepository) FindByUsername(ctx context.Context, username string) (*model.CMSUser, error) {
	for _, u := range r.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, apperr.ErrNotFound
}

func (r *FakeCMSUserRepository) Create(ctx context.Context, u *model.CMSUser) error {
	for _, existing := range r.users {
		if existing.Username == u.Username {
			return apperr.ErrConflict
		}
	}
	r.users[u.ID.String()] = u
	return nil
}

type FakeMemberRepository struct {
	members map[string]*model.Member
}

func NewFakeMemberRepository() *FakeMemberRepository {
	return &FakeMemberRepository{
		members: make(map[string]*model.Member),
	}
}

func (r *FakeMemberRepository) FindByUsername(ctx context.Context, username string) (*model.Member, error) {
	for _, m := range r.members {
		if m.Username == username {
			return m, nil
		}
	}
	return nil, apperr.ErrNotFound
}

// ===== Fake JWT Manager =====

type FakeJWTManager struct {
	policies map[string]config.ClientPolicy
}

func NewFakeJWTManager() *FakeJWTManager {
	return &FakeJWTManager{
		policies: map[string]config.ClientPolicy{
			"cms-web":     {RefreshTTL: 1 * time.Hour, AbsoluteTTL: 8 * time.Hour},
			"public-web":  {RefreshTTL: 1 * time.Hour, AbsoluteTTL: 24 * time.Hour},
			"ios-app":     {RefreshTTL: 720 * time.Hour, AbsoluteTTL: 4320 * time.Hour},
			"android-app": {RefreshTTL: 720 * time.Hour, AbsoluteTTL: 4320 * time.Hour},
		},
	}
}

func (m *FakeJWTManager) SignAccess(ctx context.Context, p jwt.SignAccessParams) (string, error) {
	return "fake_access_token", nil
}

func (m *FakeJWTManager) VerifyAccess(ctx context.Context, token string) (*jwt.AccessClaims, error) {
	return nil, nil
}

func (m *FakeJWTManager) SignRefresh(ctx context.Context, p jwt.SignRefreshParams) (string, error) {
	return "fake_refresh_token", nil
}

func (m *FakeJWTManager) VerifyRefresh(ctx context.Context, token string) (*jwt.RefreshClaims, error) {
	return nil, nil
}

func (m *FakeJWTManager) PolicyOf(ctx context.Context, clientID string) (config.ClientPolicy, error) {
	p, ok := m.policies[clientID]
	if !ok {
		return config.ClientPolicy{}, apperr.ErrInvalidClient
	}
	return p, nil
}

// ===== Fake FamilyStore =====

type FakeFamilyStore struct {
	families map[string]map[string]*redis.FamilyState
}

func NewFakeFamilyStore() *FakeFamilyStore {
	return &FakeFamilyStore{
		families: make(map[string]map[string]*redis.FamilyState),
	}
}

func (fs *FakeFamilyStore) Save(ctx context.Context, state redis.FamilyState) error {
	if fs.families[state.UserID] == nil {
		fs.families[state.UserID] = make(map[string]*redis.FamilyState)
	}
	fs.families[state.UserID][state.FamilyID] = &state
	return nil
}

func (fs *FakeFamilyStore) Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string, graceWindow time.Duration) (redis.RotateResult, *redis.FamilyState, error) {
	userFamilies, ok := fs.families[userID]
	if !ok {
		return redis.FamilyNotFound, nil, nil
	}
	state, ok := userFamilies[fid]
	if !ok {
		return redis.FamilyNotFound, nil, nil
	}
	if state.CurrentJTI == presentedJTI {
		state.CurrentJTI = newJTI
		state.LastRotatedAt = time.Now().Unix()
		return redis.Rotated, state, nil
	}
	if state.PreviousJTI == presentedJTI && state.PreviousResponseUntil > time.Now().Unix() {
		return redis.GraceHit, state, nil
	}
	return redis.ReplayDetected, nil, nil
}

func (fs *FakeFamilyStore) Revoke(ctx context.Context, userID, fid string) error {
	if fs.families[userID] != nil {
		delete(fs.families[userID], fid)
	}
	return nil
}

func (fs *FakeFamilyStore) RevokeAll(ctx context.Context, userID string) error {
	delete(fs.families, userID)
	return nil
}

func (fs *FakeFamilyStore) ListByUser(ctx context.Context, userID string) ([]redis.FamilyState, error) {
	var states []redis.FamilyState
	if families, ok := fs.families[userID]; ok {
		for _, state := range families {
			states = append(states, *state)
		}
	}
	return states, nil
}

func (fs *FakeFamilyStore) ScriptsLoaded() bool {
	return true
}

// ===== Fake Blacklist =====

type FakeBlacklist struct {
	blacklist map[string]time.Duration
}

func NewFakeBlacklist() *FakeBlacklist {
	return &FakeBlacklist{
		blacklist: make(map[string]time.Duration),
	}
}

func (bl *FakeBlacklist) Add(ctx context.Context, jti string, ttl time.Duration) error {
	bl.blacklist[jti] = ttl
	return nil
}

func (bl *FakeBlacklist) IsBlacklisted(ctx context.Context, jti string) (bool, error) {
	_, ok := bl.blacklist[jti]
	return ok, nil
}

// ===== Test Helpers =====

func setupTestRouter(t *testing.T) (*gin.Engine, *AuthHandler) {
	gin.SetMode(gin.TestMode)
	logger.Init(config.LogConfig{Format: "console", Level: "debug", Service: "test"}, "dev") //nolint:errcheck

	cmsUserRepo := NewFakeCMSUserRepository()
	memberRepo := NewFakeMemberRepository()
	jwtManager := NewFakeJWTManager()
	hasherImpl := hasher.NewBcryptHasher(12)
	familyStore := NewFakeFamilyStore()
	blacklist := NewFakeBlacklist()

	authSvc := service.NewAuthService(cmsUserRepo, memberRepo, jwtManager, hasherImpl, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute)
	handler := NewAuthHandler(authSvc)

	router := gin.New()
	router.Use(logger.RequestID())
	router.Use(logger.GinLogger())

	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", handler.Register)
		authGroup.POST("/login", handler.Login)
		authGroup.POST("/refresh", handler.Refresh)
		authGroup.POST("/logout", handler.Logout)
		authGroup.GET("/sessions", handler.ListSessions)
		authGroup.DELETE("/sessions/:fid", handler.RevokeSession)
		authGroup.POST("/sessions/revoke-all", handler.RevokeAllSessions)
	}

	return router, handler
}

// ===== Tests =====

// TestAuthHandler_Register_Success — POST /auth/register 成功注册
func TestAuthHandler_Register_Success(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username":  "testuser",
		"password":  "password123",
		"client_id": "cms-web",
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// 成功回 201（§3.5）
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestAuthHandler_Register_InvalidClient — POST /auth/register 当 client_id != cms-web
func TestAuthHandler_Register_InvalidClient(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username":  "testuser",
		"password":  "password123",
		"client_id": "ios-app", // 无效：仅 cms-web 放行
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_client", resp["error"])
}

// TestAuthHandler_Register_WeakPassword — POST /auth/register 当密码太弱
func TestAuthHandler_Register_WeakPassword(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username":  "testuser",
		"password":  "onlyletters",  // ≥8 字符但無數字 → 服務層弱密碼檢查 → 422
		"client_id": "cms-web",
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "weak_password", resp["error"])
}

// TestAuthHandler_Register_UsernameTaken — POST /auth/register 当 username 已被占用
func TestAuthHandler_Register_UsernameTaken(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 先注册一个用户
	body1 := map[string]string{
		"username":  "existing",
		"password":  "password123",
		"client_id": "cms-web",
	}
	bodyBytes1, _ := json.Marshal(body1)
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes1))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)

	// 再用同样 username 注册
	body2 := map[string]string{
		"username":  "existing",
		"password":  "password123",
		"client_id": "cms-web",
	}
	bodyBytes2, _ := json.Marshal(body2)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes2))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusConflict, w2.Code)

	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, "username_taken", resp["error"])
}

// TestAuthHandler_Register_InvalidInput — POST /auth/register 当缺少必填字段
func TestAuthHandler_Register_InvalidInput(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username": "testuser",
		// 缺少 password 和 client_id
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid input", resp["error"])
}

// TestAuthHandler_Login_Success_CMSUser — POST /auth/login CMS user 成功登入
func TestAuthHandler_Login_Success_CMSUser(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 先注册一个 CMS user
	registerBody := map[string]string{
		"username":  "cmsuser",
		"password":  "password123",
		"client_id": "cms-web",
	}
	registerBytes, _ := json.Marshal(registerBody)
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(registerBytes))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)

	// 登入
	loginBody := map[string]string{
		"username":  "cmsuser",
		"password":  "password123",
		"client_id": "cms-web",
	}
	loginBytes, _ := json.Marshal(loginBody)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/auth/login", bytes.NewReader(loginBytes))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, true, resp["success"])
	assert.NotNil(t, resp["data"])

	data := resp["data"].(map[string]interface{})
	assert.NotEmpty(t, data["access_token"])
	assert.NotEmpty(t, data["refresh_token"])
	assert.Equal(t, "Bearer", data["token_type"])
}

// TestAuthHandler_Login_InvalidCredentials — POST /auth/login 当密码错误
func TestAuthHandler_Login_InvalidCredentials(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 先注册一个用户
	registerBody := map[string]string{
		"username":  "cmsuser",
		"password":  "password123",
		"client_id": "cms-web",
	}
	registerBytes, _ := json.Marshal(registerBody)
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(registerBytes))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)

	// 用错误密码登入
	loginBody := map[string]string{
		"username":  "cmsuser",
		"password":  "wrongpassword",
		"client_id": "cms-web",
	}
	loginBytes, _ := json.Marshal(loginBody)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/auth/login", bytes.NewReader(loginBytes))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusUnauthorized, w2.Code)

	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

// TestAuthHandler_Login_UserNotFound — POST /auth/login 当用户不存在
func TestAuthHandler_Login_UserNotFound(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username":  "nonexistent",
		"password":  "password123",
		"client_id": "cms-web",
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

// TestAuthHandler_Login_InvalidClient — POST /auth/login 当 client_id 无效
func TestAuthHandler_Login_InvalidClient(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username":  "someuser",
		"password":  "password123",
		"client_id": "unknown-client",
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_client", resp["error"])
}

// TestAuthHandler_ResponseEnvelope — 验证响应符合 §3.5 / §10 envelope 格式
func TestAuthHandler_ResponseEnvelope(t *testing.T) {
	router, _ := setupTestRouter(t)

	registerBody := map[string]string{
		"username":  "testuser",
		"password":  "password123",
		"client_id": "cms-web",
	}
	bodyBytes, _ := json.Marshal(registerBody)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "test-request-id-123")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

// TestAuthHandler_ErrorResponseEnvelope — 验证错误响应符合 §3.5 / §12.1 envelope 格式
func TestAuthHandler_ErrorResponseEnvelope(t *testing.T) {
	router, _ := setupTestRouter(t)

	body := map[string]string{
		"username": "testuser",
		// 缺少必填字段
	}
	bodyBytes, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "test-request-id-456")
	router.ServeHTTP(w, req)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// 验证错误 envelope 结构（§3.5 / §12.1）
	assert.Equal(t, false, resp["success"])
	assert.NotEmpty(t, resp["request_id"])
	assert.NotEmpty(t, resp["error"])
}

// TestAuthHandler_Logout_MissingAuth — POST /auth/logout 无认证时
func TestAuthHandler_Logout_MissingAuth(t *testing.T) {
	router, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// 缺少 auth header
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

// TestAuthHandler_ListSessions_MissingAuth — GET /auth/sessions 无认证时
func TestAuthHandler_ListSessions_MissingAuth(t *testing.T) {
	router, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sessions", nil)
	router.ServeHTTP(w, req)

	// 缺少 auth header
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

// TestAuthHandler_RevokeSession_MissingAuth — DELETE /auth/sessions/{fid} 无认证时
func TestAuthHandler_RevokeSession_MissingAuth(t *testing.T) {
	router, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/auth/sessions/some-fid", nil)
	router.ServeHTTP(w, req)

	// 缺少 auth header
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

// TestAuthHandler_RevokeAllSessions_MissingAuth — POST /auth/sessions/revoke-all 无认证时
func TestAuthHandler_RevokeAllSessions_MissingAuth(t *testing.T) {
	router, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/sessions/revoke-all", nil)
	router.ServeHTTP(w, req)

	// 缺少 auth header
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized", resp["error"])
}
