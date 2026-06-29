package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okPing / failPing 為注入 HealthHandler 的 fake 依賴探測函式。
func okPing(context.Context) error   { return nil }
func failPing(context.Context) error { return errors.New("dependency down") }

// doReady 以指定 handler 跑一次 GET /health/ready，回傳 status code 與解析後的元件狀態。
func doReady(t *testing.T, h *HealthHandler) (int, map[string]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Ready(ctx)

	var resp struct {
		Status map[string]string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return w.Code, resp.Status
}

// TestHealthHandler_Live_AlwaysOK 驗證存活探測不檢查依賴，永遠回 200 ok。
func TestHealthHandler_Live_AlwaysOK(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	(&HealthHandler{}).Live(ctx)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])
}

// TestHealthHandler_Ready_AllHealthy_Returns200 驗證所有依賴健康時回 200 且每個元件皆 ok。
func TestHealthHandler_Ready_AllHealthy_Returns200(t *testing.T) {
	h := &HealthHandler{
		pingDB:      okPing,
		pingRedis:   okPing,
		familyReady: func() bool { return true },
	}

	code, status := doReady(t, h)

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", status["database"])
	assert.Equal(t, "ok", status["redis"])
	assert.Equal(t, "ok", status["family_store_scripts"])
}

// TestHealthHandler_Ready_PostgresDown_Returns503 驗證 PG 不健康時回 503，且只有 database 標記 unhealthy。
func TestHealthHandler_Ready_PostgresDown_Returns503(t *testing.T) {
	h := &HealthHandler{
		pingDB:      failPing,
		pingRedis:   okPing,
		familyReady: func() bool { return true },
	}

	code, status := doReady(t, h)

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "unhealthy", status["database"])
	assert.Equal(t, "ok", status["redis"])
	assert.Equal(t, "ok", status["family_store_scripts"])
}

// TestHealthHandler_Ready_RedisDown_Returns503 驗證 Redis 不健康時回 503，且只有 redis 標記 unhealthy。
func TestHealthHandler_Ready_RedisDown_Returns503(t *testing.T) {
	h := &HealthHandler{
		pingDB:      okPing,
		pingRedis:   failPing,
		familyReady: func() bool { return true },
	}

	code, status := doReady(t, h)

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "ok", status["database"])
	assert.Equal(t, "unhealthy", status["redis"])
	assert.Equal(t, "ok", status["family_store_scripts"])
}

// TestHealthHandler_Ready_FamilyStoreNotLoaded_Returns503 驗證 Lua 腳本未載入時回 503。
func TestHealthHandler_Ready_FamilyStoreNotLoaded_Returns503(t *testing.T) {
	h := &HealthHandler{
		pingDB:      okPing,
		pingRedis:   okPing,
		familyReady: func() bool { return false },
	}

	code, status := doReady(t, h)

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "ok", status["database"])
	assert.Equal(t, "ok", status["redis"])
	assert.Equal(t, "unhealthy", status["family_store_scripts"])
}

// TestHealthHandler_Ready_AllDown_Returns503 驗證多依賴同時失敗時，所有元件皆標記 unhealthy。
func TestHealthHandler_Ready_AllDown_Returns503(t *testing.T) {
	h := &HealthHandler{
		pingDB:      failPing,
		pingRedis:   failPing,
		familyReady: func() bool { return false },
	}

	code, status := doReady(t, h)

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "unhealthy", status["database"])
	assert.Equal(t, "unhealthy", status["redis"])
	assert.Equal(t, "unhealthy", status["family_store_scripts"])
}

// TestHealthHandler_Ready_NilDependencies_Returns200 驗證未設定的依賴（nil ping）視為健康。
func TestHealthHandler_Ready_NilDependencies_Returns200(t *testing.T) {
	h := &HealthHandler{} // 全 nil：模擬無 DB / Redis / FamilyStore 設定

	code, status := doReady(t, h)

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", status["database"])
	assert.Equal(t, "ok", status["redis"])
	assert.Equal(t, "ok", status["family_store_scripts"])
}
