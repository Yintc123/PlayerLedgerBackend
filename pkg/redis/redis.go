package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/yintengching/playerledger/config"
)

// Connect 建立 Redis 连接。
// 根据规格书§7.2，包含连接参数、timeout、pool 配置。
func Connect(cfg config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
	})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()

	if _, err := client.Ping(ctx).Result(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return client, nil
}

// Close 关闭 Redis 连接。
func Close(client *redis.Client) error {
	return client.Close()
}
