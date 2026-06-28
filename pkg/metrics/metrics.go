package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, path, status",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method, path, and status",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	RateLimiterErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "ratelimit_errors_total",
			Help: "Rate limiter backend errors (Redis unavailable, etc.)",
		},
	)

	RateLimitMisconfigured = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ratelimit_misconfigured_total",
			Help: "UserMiddleware invoked without claims — middleware order misconfigured",
		},
		[]string{"path"},
	)

	AuthLoginAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_login_attempts_total",
			Help: "Login attempts by result",
		},
		[]string{"result"},
	)

	AuthRotations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_token_rotations_total",
			Help: "Refresh token rotations by result",
		},
		[]string{"result", "client_id"},
	)

	AuthReplayDetected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_replay_detected_total",
			Help: "Refresh token replay attacks detected",
		},
		[]string{"client_id"},
	)

	AuthBlacklistErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "auth_blacklist_errors_total",
			Help: "Errors querying access-token blacklist in AuthMiddleware (fail-open path)",
		},
	)

	BuildInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "build_info",
			Help: "Build information",
		},
		[]string{"version", "commit", "build_time"},
	)
)

// Init 注册应用指标
func Init(sqlDB *sql.DB, version, commit, buildTime string) {
	// SQL 连接池指标
	if sqlDB != nil {
		prometheus.MustRegister(collectors.NewDBStatsCollector(sqlDB, "main"))
	}

	// Build info
	BuildInfo.WithLabelValues(version, commit, buildTime).Set(1)
}
