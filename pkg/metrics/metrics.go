package metrics

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// 所有 metric 變數以 prometheus.NewXxx 建立，不自動註冊。
// 須在 Init() 中顯式 MustRegister（§18.2）。
var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, path, status",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method, path, and status",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	RateLimiterErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ratelimit_errors_total",
			Help: "Rate limiter backend errors (Redis unavailable, etc.)",
		},
	)

	RateLimitMisconfigured = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ratelimit_misconfigured_total",
			Help: "UserMiddleware invoked without claims — middleware order misconfigured",
		},
		[]string{"path"},
	)

	AuthLoginAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_login_attempts_total",
			Help: "Login attempts by result",
		},
		[]string{"result"},
	)

	AuthRotations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_token_rotations_total",
			Help: "Refresh token rotations by result",
		},
		[]string{"result", "client_id"},
	)

	AuthReplayDetected = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_replay_detected_total",
			Help: "Refresh token replay attacks detected",
		},
		[]string{"client_id"},
	)

	AuthBlacklistErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "auth_blacklist_errors_total",
			Help: "Errors querying access-token blacklist in AuthMiddleware (fail-open path)",
		},
	)

	// BuildInfo 使用正確名稱 app_build_info 及正確 labels（§18.2）。
	BuildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "app_build_info",
			Help: "Build information",
		},
		[]string{"version", "commit"},
	)
)

// Init 顯式 MustRegister 所有 metric（§18.2）。
// sqlDB 若為 nil 則跳過 DBStatsCollector 的收集。
func Init(sqlDB *sql.DB, version, commit string) {
	prometheus.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		RateLimiterErrors,
		RateLimitMisconfigured,
		AuthLoginAttempts,
		AuthRotations,
		AuthReplayDetected,
		AuthBlacklistErrors,
		BuildInfo,
		collectors.NewBuildInfoCollector(),
	)

	if sqlDB != nil {
		prometheus.MustRegister(collectors.NewDBStatsCollector(sqlDB, "main"))
	}

	BuildInfo.WithLabelValues(version, commit).Set(1)
}

// GinMiddleware 記錄每個 request 的指標（§18.2）。
// path 一律用 c.FullPath()（例 /players/:id）避免高基數爆 Prometheus。
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}
		status := strconv.Itoa(c.Writer.Status())
		HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		HTTPRequestDuration.WithLabelValues(c.Request.Method, path, status).Observe(time.Since(start).Seconds())
	}
}
