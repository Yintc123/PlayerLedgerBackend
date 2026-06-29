package ratelimit

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// NewRedisStore 在建構時會 preload Lua script，需要可達的 Redis。
// 正常路徑屬 integration（需真實 Redis）；此處離線驗證「Redis 不可達時錯誤被傳回」，
// 對齊 pkg/redis 既有的錯誤路徑單元測試慣例。
func TestNewRedisStore_UnreachableRedis_ReturnsError(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:55555", // 不存在的 port
		DialTimeout: 100 * time.Millisecond,
	})
	defer client.Close()

	store, err := NewRedisStore(client, "")
	assert.Error(t, err, "Redis 不可達時 NewRedisStore 應回錯誤")
	assert.Nil(t, store)
}
