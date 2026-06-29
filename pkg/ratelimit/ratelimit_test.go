package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"github.com/yintengching/playerledger/pkg/jwt"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// errStore 永遠回錯，用以驗證 fail-open。
type errStore struct{}

func (errStore) Get(context.Context, string, limiter.Rate) (limiter.Context, error) {
	return limiter.Context{}, errors.New("boom")
}
func (errStore) Peek(context.Context, string, limiter.Rate) (limiter.Context, error) {
	return limiter.Context{}, errors.New("boom")
}
func (errStore) Reset(context.Context, string, limiter.Rate) (limiter.Context, error) {
	return limiter.Context{}, errors.New("boom")
}
func (errStore) Increment(context.Context, string, int64, limiter.Rate) (limiter.Context, error) {
	return limiter.Context{}, errors.New("boom")
}

func okHandler(c *gin.Context) { c.String(http.StatusOK, "ok") }

func doGet(r http.Handler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestIPMiddleware_AllowsUpToLimitThenBlocks(t *testing.T) {
	r := gin.New()
	r.Use(IPMiddleware(time.Minute, 2, memory.NewStore()))
	r.GET("/", okHandler)

	assert.Equal(t, http.StatusOK, doGet(r).Code)
	assert.Equal(t, http.StatusOK, doGet(r).Code)

	w := doGet(r)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"), "429 應帶 Retry-After")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, false, body["success"])
	assert.Equal(t, "too many requests", body["error"])
}

func TestIPMiddleware_FailOpen_OnStoreError(t *testing.T) {
	r := gin.New()
	r.Use(IPMiddleware(time.Minute, 1, errStore{}))
	r.GET("/", okHandler)

	// store 故障時應放行（fail-open），而非 500 / 429。
	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, doGet(r).Code)
	}
}

func TestUserMiddleware_NoClaims_PassesThrough(t *testing.T) {
	// 沒有 claims（key=""）時放行，不套用限流——即使 limit=1 也不會 429。
	r := gin.New()
	r.Use(UserMiddleware(time.Minute, 1, memory.NewStore()))
	r.GET("/", okHandler)

	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, doGet(r).Code)
	}
}

func TestUserMiddleware_WithClaims_LimitsByUser(t *testing.T) {
	store := memory.NewStore()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		jwt.SetClaims(c, &jwt.AccessClaims{
			RegisteredClaims: gojwt.RegisteredClaims{Subject: "user-1"},
		})
		c.Next()
	})
	r.Use(UserMiddleware(time.Minute, 1, store))
	r.GET("/", okHandler)

	assert.Equal(t, http.StatusOK, doGet(r).Code)
	assert.Equal(t, http.StatusTooManyRequests, doGet(r).Code)
}
