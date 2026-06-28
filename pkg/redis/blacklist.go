package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// AccessTokenBlacklist 定义访问令牌黑名单接口。
// 用于强制踢人场景，日常验证不走黑名单（stateless）。
type AccessTokenBlacklist interface {
	// Add 将 jti 加入黑名单，存活 ttl 秒。
	// ttl ≤ 0 时 no-op（token 已自然过期）。
	// Redis 写入失败返回 error，caller 应 log + metric 但不影响关键步骤。
	Add(ctx context.Context, jti string, ttl time.Duration) error

	// IsBlacklisted 查询 jti 是否在黑名单。
	// 返回 (bool, error)：
	// - (false, nil)：不在黑名单
	// - (true, nil)：在黑名单
	// - (false, err)：Redis 故障，caller fail-open
	IsBlacklisted(ctx context.Context, jti string) (bool, error)
}

type accessTokenBlacklist struct {
	client *redis.Client
}

// NewAccessTokenBlacklist 创建黑名单实例。
func NewAccessTokenBlacklist(client *redis.Client) AccessTokenBlacklist {
	return &accessTokenBlacklist{client: client}
}

// Add 实现 AccessTokenBlacklist.Add。
// 使用 SETEX：key=auth:blacklist:<jti>，value=1，expiry=ttl。
func (b *accessTokenBlacklist) Add(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}

	key := "auth:blacklist:" + jti
	if err := b.client.Set(ctx, key, "1", ttl).Err(); err != nil {
		return err
	}
	return nil
}

// IsBlacklisted 实现 AccessTokenBlacklist.IsBlacklisted。
// 使用 EXISTS：存在返回 true，不存在返回 false。
func (b *accessTokenBlacklist) IsBlacklisted(ctx context.Context, jti string) (bool, error) {
	key := "auth:blacklist:" + jti
	count, err := b.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
