package ratelimit

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
	"github.com/yintengching/playerledger/pkg/metrics"
	"github.com/ulule/limiter/v3"
	"go.uber.org/zap"
)

// IPMiddleware 以 c.ClientIP() 为限流 key（§15.2）
// 掛在 /api/v1，所有人都受限（匿名 + 已認證）
func IPMiddleware(period time.Duration, limit int64, store limiter.Store) gin.HandlerFunc {
	return newMiddleware(period, limit, store, func(c *gin.Context) string {
		return "ratelimit:ip:" + c.ClientIP()
	})
}

// UserMiddleware 以 claims.UserID() 为限流 key（§15.2）
// 必须掛在 AuthMiddleware 之后；若无 claims 视为 misconfiguration
func UserMiddleware(period time.Duration, limit int64, store limiter.Store) gin.HandlerFunc {
	return newMiddleware(period, limit, store, func(c *gin.Context) string {
		claims, ok := jwt.GetClaims(c)
		if !ok {
			logger.L().Error("UserMiddleware without claims — middleware order misconfigured",
				zap.String("request_id", logger.GetRequestID(c)),
				zap.String("path", c.FullPath()),
			)
			metrics.RateLimitMisconfigured.WithLabelValues(c.FullPath()).Inc()
			return ""
		}
		// hash tag {userID} 对齐 §7.1
		return "ratelimit:user:{" + claims.UserID() + "}"
	})
}

// newMiddleware 通用限流中间件（fail-open，§15.2）
func newMiddleware(period time.Duration, limit int64, store limiter.Store,
	keyFn func(*gin.Context) string) gin.HandlerFunc {

	rate := limiter.Rate{Period: period, Limit: limit}
	lim := limiter.New(store, rate)

	return func(c *gin.Context) {
		key := keyFn(c)
		if key == "" {
			c.Next()
			return
		}

		ctx, err := lim.Get(c.Request.Context(), key)
		if err != nil {
			// Fail-open：limiter 故障时放行，记 warn 供監控
			logger.L().Warn("rate limiter failed, allowing request",
				zap.String("request_id", logger.GetRequestID(c)),
				zap.Error(err),
			)
			metrics.RateLimiterErrors.Inc()
			c.Next()
			return
		}

		if ctx.Reached {
			// 计算實際剩余秒数（避免客户端误解时戳）
			retryAfter := ctx.Reset - time.Now().Unix()
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))

			c.AbortWithStatusJSON(429, gin.H{
				"success":    false,
				"request_id": logger.GetRequestID(c),
				"error":      "too many requests",
			})
			return
		}

		c.Next()
	}
}
