package ratelimit

import (
	"github.com/redis/go-redis/v9"
	"github.com/ulule/limiter/v3"
	sredis "github.com/ulule/limiter/v3/drivers/store/redis"
)

// NewRedisStore 以同一个 *redis.Client 建构 limiter store（§15.4）
// prefix 预设 "ratelimit"；对应 §15.3 / §7.1 key 命名
func NewRedisStore(client *redis.Client, prefix string) (limiter.Store, error) {
	if prefix == "" {
		prefix = "ratelimit"
	}
	return sredis.NewStoreWithOptions(client, limiter.StoreOptions{
		Prefix:   prefix,
		MaxRetry: 3,
	})
}
