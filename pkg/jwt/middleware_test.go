package jwt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/pkg/metrics"
)

// testMetricValue 讀取 prometheus counter 的當前累計值（測試輔助）。
func testMetricValue(c prometheus.Counter) float64 {
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// fakeBlacklist 為 AuthMiddleware 測試用的記憶體 AccessTokenBlacklist 替身。
type fakeBlacklist struct {
	hit bool
	err error
}

func (f *fakeBlacklist) Add(ctx context.Context, jti string, ttl time.Duration) error {
	return nil
}
func (f *fakeBlacklist) IsBlacklisted(ctx context.Context, jti string) (bool, error) {
	return f.hit, f.err
}

// fakeUserRevocationStore 為 AuthMiddleware 測試用的記憶體 UserRevocationStore 替身。
type fakeUserRevocationStore struct {
	watermark int64
	err       error
	calls     int
}

func (f *fakeUserRevocationStore) Revoke(ctx context.Context, userID string, ttl time.Duration) error {
	return nil
}
func (f *fakeUserRevocationStore) RevokedAfter(ctx context.Context, userID string) (int64, error) {
	f.calls++
	return f.watermark, f.err
}

// newTestRouter 組裝一個只掛 AuthMiddleware 的最小 gin router，handler 簡單回 200。
func newTestRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// signValidAccessToken 簽一個有效 access token，回傳 (token, claims)。
func signValidAccessToken(t *testing.T, mgr Manager, userID string) (string, *AccessClaims) {
	t.Helper()
	ctx := context.Background()
	params := SignAccessParams{
		UserID:   userID,
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-mw-1",
		ClientID: "cms-web",
		JTI:      "jti-mw-1",
		TTL:      15 * time.Minute,
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)
	claims, err := mgr.VerifyAccess(ctx, token)
	require.NoError(t, err)
	return token, claims
}

// decodeErrorBody 取出回應 JSON 的 error 欄位。
func decodeErrorBody(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Success   bool   `json:"success"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Error
}

// TestAuthMiddleware_NoAuthHeader_Unauthorized 缺 Authorization header 回 401 unauthorized
func TestAuthMiddleware_NoAuthHeader_Unauthorized(t *testing.T) {
	mgr := NewManager(newTestConfig())
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeErrorBody(t, w.Body.Bytes()))
}

// TestAuthMiddleware_BadBearerPrefix_Unauthorized prefix 錯誤回 401 unauthorized
func TestAuthMiddleware_BadBearerPrefix_Unauthorized(t *testing.T) {
	mgr := NewManager(newTestConfig())
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Token abc.def.ghi")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeErrorBody(t, w.Body.Bytes()))
}

// TestAuthMiddleware_InvalidToken_InvalidTokenCode 簽章錯誤回 401 invalid_token
func TestAuthMiddleware_InvalidToken_InvalidTokenCode(t *testing.T) {
	mgr := NewManager(newTestConfig())
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not.a.real.jwt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "invalid_token", decodeErrorBody(t, w.Body.Bytes()))
}

// TestAuthMiddleware_ValidToken_Passes 簽證有效時放行
func TestAuthMiddleware_ValidToken_Passes(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, _ := signValidAccessToken(t, mgr, "user-mw-ok")

	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestAuthMiddleware_BlacklistHit_SessionRevoked jti 命中黑名單回 401 session_revoked
func TestAuthMiddleware_BlacklistHit_SessionRevoked(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, _ := signValidAccessToken(t, mgr, "user-bl-hit")

	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{hit: true}, &fakeUserRevocationStore{}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "session_revoked", decodeErrorBody(t, w.Body.Bytes()))
}

// TestAuthMiddleware_BlacklistError_FailOpen 黑名單查詢錯誤時 fail-open（放行）
func TestAuthMiddleware_BlacklistError_FailOpen(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, _ := signValidAccessToken(t, mgr, "user-bl-err")

	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{err: errors.New("redis down")}, &fakeUserRevocationStore{}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "blacklist err → fail-open (pass)")
}

// TestAuthMiddleware_UserRevoke_NoWatermark_Passes user 從未被 revoke 時放行（§7.5 / §8.5 step 3.5）
func TestAuthMiddleware_UserRevoke_NoWatermark_Passes(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, _ := signValidAccessToken(t, mgr, "user-revoke-none")

	revoke := &fakeUserRevocationStore{watermark: 0}
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, revoke))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, revoke.calls, "RevokedAfter must be queried once per request")
}

// TestAuthMiddleware_UserRevoke_IatBeforeWatermark_SessionRevoked claims.iat < watermark 回 401 session_revoked
func TestAuthMiddleware_UserRevoke_IatBeforeWatermark_SessionRevoked(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, claims := signValidAccessToken(t, mgr, "user-revoke-stale")

	// 設 watermark = iat + 1，模擬 admin 在簽 token 之後才踢人
	watermark := claims.IssuedAt.Time.Unix() + 1
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{watermark: watermark}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "session_revoked", decodeErrorBody(t, w.Body.Bytes()))
}

// TestAuthMiddleware_UserRevoke_IatAfterWatermark_Passes claims.iat ≥ watermark 時放行（revoke 之後簽的 token 合法）
func TestAuthMiddleware_UserRevoke_IatAfterWatermark_Passes(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, claims := signValidAccessToken(t, mgr, "user-revoke-after")

	// 設 watermark = iat - 60，模擬 token 在 admin 踢人之後才簽
	watermark := claims.IssuedAt.Time.Unix() - 60
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{watermark: watermark}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestAuthMiddleware_UserRevoke_IatEqualsWatermark_Passes claims.iat == watermark 時放行（嚴格小於才視為過期）
func TestAuthMiddleware_UserRevoke_IatEqualsWatermark_Passes(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, claims := signValidAccessToken(t, mgr, "user-revoke-eq")

	watermark := claims.IssuedAt.Time.Unix()
	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{watermark: watermark}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "iat == watermark → pass (only iat < watermark is revoked)")
}

// TestAuthMiddleware_UserRevoke_Error_FailOpenWithMetric Redis 故障時 fail-open 且增加 metric
func TestAuthMiddleware_UserRevoke_Error_FailOpenWithMetric(t *testing.T) {
	mgr := NewManager(newTestConfig())
	token, _ := signValidAccessToken(t, mgr, "user-revoke-err")

	before := testMetricValue(metrics.AuthUserRevokeErrors)

	r := newTestRouter(AuthMiddleware(mgr, &fakeBlacklist{}, &fakeUserRevocationStore{err: errors.New("redis down")}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "user-revoke err → fail-open (pass)")
	after := testMetricValue(metrics.AuthUserRevokeErrors)
	assert.InDelta(t, before+1, after, 0.0001, "AuthUserRevokeErrors must increment on fail-open")
}
