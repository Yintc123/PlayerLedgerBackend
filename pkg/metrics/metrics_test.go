package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// 不呼叫 Init()（會對全域 registry MustRegister，重複註冊將 panic）；
// CounterVec 即使未註冊，testutil.ToFloat64 仍可讀值。
func TestGinMiddleware_IncrementsRequestCounter(t *testing.T) {
	r := gin.New()
	r.Use(GinMiddleware())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	counter := HTTPRequestsTotal.WithLabelValues(http.MethodGet, "/ping", "200")
	before := testutil.ToFloat64(counter)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	after := testutil.ToFloat64(counter)
	assert.Equal(t, before+1, after, "GET /ping 200 計數應 +1")
}

func TestGinMiddleware_ObservesDuration(t *testing.T) {
	r := gin.New()
	r.Use(GinMiddleware())
	r.GET("/timed", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/timed", nil)
	r.ServeHTTP(w, req)

	// histogram 對應 label 應至少有一筆觀測（count == 樣本數）。
	count := testutil.CollectAndCount(HTTPRequestDuration)
	assert.GreaterOrEqual(t, count, 1)
}

func TestHandler_ServesMetrics200(t *testing.T) {
	r := gin.New()
	r.GET("/metrics", Handler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Body.String())
}
