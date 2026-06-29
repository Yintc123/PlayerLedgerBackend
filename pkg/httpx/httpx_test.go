package httpx

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func TestTernaryInt64(t *testing.T) {
	assert.Equal(t, int64(31536000), ternaryInt64(true, 31536000, 0))
	assert.Equal(t, int64(0), ternaryInt64(false, 31536000, 0))
}

func TestMaxBodyBytes_UnderLimit_ReadsFully(t *testing.T) {
	r := gin.New()
	r.Use(MaxBodyBytes(1024))
	r.POST("/", func(c *gin.Context) {
		b, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, string(b))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello", w.Body.String())
}

func TestMaxBodyBytes_OverLimit_ReadErrors(t *testing.T) {
	r := gin.New()
	r.Use(MaxBodyBytes(4))
	var readErr error
	r.POST("/", func(c *gin.Context) {
		_, readErr = io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123456789"))
	r.ServeHTTP(w, req)

	require.Error(t, readErr, "超過 MaxBodyBytes 後讀取 body 應回錯誤")
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestWriteError_ShapeAndStatus(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	WriteError(c, http.StatusNotFound, "not found")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.True(t, c.IsAborted())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, false, body["success"])
	assert.Equal(t, "not found", body["error"])
	_, hasReqID := body["request_id"]
	assert.True(t, hasReqID)
	_, hasDetails := body["details"]
	assert.False(t, hasDetails, "無 details 欄位")
}

func TestWriteErrorWithDetails_IncludesDetails(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	WriteErrorWithDetails(c, http.StatusUnprocessableEntity, "validation failed", []FieldError{
		{Field: "amount", Message: "must be > 0"},
	})

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "validation failed", body["error"])
	details, ok := body["details"].([]any)
	require.True(t, ok)
	require.Len(t, details, 1)
}

func TestWriteErrorWithDetails_EmptyDetails_Omitted(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	WriteErrorWithDetails(c, http.StatusBadRequest, "bad", nil)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	_, hasDetails := body["details"]
	assert.False(t, hasDetails, "空 details 不應輸出該欄位")
}

func TestStatusNoContent(t *testing.T) {
	r := gin.New()
	r.DELETE("/", func(c *gin.Context) { StatusNoContent(c) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestGinRecovery_PanicBecomes500(t *testing.T) {
	r := gin.New()
	r.Use(GinRecovery())
	r.GET("/boom", func(c *gin.Context) {
		panic("kaboom")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, false, body["success"])
	assert.Equal(t, "internal server error", body["error"])
}

func TestSecureHeaders_Prod_SetsHSTS(t *testing.T) {
	r := gin.New()
	r.Use(SecureHeaders("prod"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// unrolled/secure 只在 TLS 請求發 HSTS；模擬 HTTPS。
	req.TLS = &tls.ConnectionState{}
	r.ServeHTTP(w, req)

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=31536000")
}

func TestSecureHeaders_Dev_NoHSTS(t *testing.T) {
	r := gin.New()
	r.Use(SecureHeaders("dev"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Empty(t, w.Header().Get("Strict-Transport-Security"), "dev 不應發 HSTS")
}
