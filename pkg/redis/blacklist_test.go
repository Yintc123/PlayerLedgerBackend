package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRedisClient 创建测试用 Redis client，从环境变量读取连接信息
func newTestRedisClient(t *testing.T) *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	password := os.Getenv("REDIS_PASSWORD")

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})

	// 测试连接是否成功
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable at %s: %v", addr, err)
	}

	return client
}

// TestAccessTokenBlacklistAdd_ValidJTIAndTTL_SetexSucceeds 测试有效 JTI 和 TTL 成功写入
func TestAccessTokenBlacklistAdd_ValidJTIAndTTL_SetexSucceeds(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	// 清理测试前后的 Redis 状态
	ctx := context.Background()
	key := "auth:blacklist:test-jti-123"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-jti-123"
	ttl := 10 * time.Minute

	err := blacklist.Add(ctx, jti, ttl)
	require.NoError(t, err, "Add should succeed with valid JTI and TTL")

	// 验证 key 确实存在
	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "key should exist in Redis")

	// 验证 value 是 "1"
	val, err := client.Get(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", val, "value should be '1'")
}

// TestAccessTokenBlacklistAdd_ZeroTTL_NoOp 测试 TTL≤0 时不写入（no-op）
func TestAccessTokenBlacklistAdd_ZeroTTL_NoOp(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:test-zero-ttl"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-zero-ttl"

	// TTL = 0：no-op
	err := blacklist.Add(ctx, jti, 0)
	require.NoError(t, err, "Add with TTL=0 should return no error (no-op)")

	// 验证 key 不存在
	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key should not exist (no-op for TTL≤0)")
}

// TestAccessTokenBlacklistAdd_NegativeTTL_NoOp 测试 TTL<0 时不写入
func TestAccessTokenBlacklistAdd_NegativeTTL_NoOp(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:test-negative-ttl"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-negative-ttl"

	// TTL < 0：no-op
	err := blacklist.Add(ctx, jti, -5*time.Minute)
	require.NoError(t, err, "Add with negative TTL should return no error (no-op)")

	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key should not exist (no-op for TTL<0)")
}

// TestAccessTokenBlacklistAdd_TTLExpiry_KeyEvicted 测试 TTL 到期后 key 自动过期
func TestAccessTokenBlacklistAdd_TTLExpiry_KeyEvicted(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:test-expiry"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-expiry"
	ttl := 500 * time.Millisecond // 短 TTL 便于测试

	err := blacklist.Add(ctx, jti, ttl)
	require.NoError(t, err)

	// 立即验证存在
	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "key should exist immediately after Add")

	// 等待 TTL 过期
	time.Sleep(600 * time.Millisecond)

	// 验证已过期
	exists, err = client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key should be evicted after TTL expires")
}

// TestAccessTokenBlacklistIsBlacklisted_KeyExists_ReturnsTrue 测试 key 存在时返回 true
func TestAccessTokenBlacklistIsBlacklisted_KeyExists_ReturnsTrue(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:test-exists"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-exists"

	// 手动写入 key
	_ = client.Set(ctx, key, "1", 10*time.Minute)

	// 查询
	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err, "IsBlacklisted should not return error")
	assert.True(t, isBlacklisted, "should return true when key exists")
}

// TestAccessTokenBlacklistIsBlacklisted_KeyNotExists_ReturnsFalseNil 测试 key 不存在时返回 (false, nil)
func TestAccessTokenBlacklistIsBlacklisted_KeyNotExists_ReturnsFalseNil(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:nonexistent-key-xyz"
	_ = client.Del(ctx, key)

	blacklist := NewAccessTokenBlacklist(client)
	jti := "nonexistent-key-xyz"

	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err, "IsBlacklisted should not return error for missing key")
	assert.False(t, isBlacklisted, "should return false when key does not exist")
}

// TestAccessTokenBlacklistIsBlacklisted_FailOpen_RedisConnFailureReturnsError 测试 Redis 连接故障时返回 error（fail-open）
func TestAccessTokenBlacklistIsBlacklisted_FailOpen_RedisConnFailureReturnsError(t *testing.T) {
	// 创建一个指向不存在服务的 client，模拟连接故障
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:55555", // 不存在的端口
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
	})
	defer client.Close()

	ctx := context.Background()
	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-jti"

	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)

	// Fail-open 行为：Redis 故障时返回 error（不是 panic，也不是 true）
	assert.Error(t, err, "IsBlacklisted should return error when Redis is unavailable")
	assert.False(t, isBlacklisted, "should return false even when Redis fails (fail-open principle)")
}

// TestAccessTokenBlacklistIsBlacklisted_ContextTimeout_ReturnsError 测试 context timeout 时返回 error
func TestAccessTokenBlacklistIsBlacklisted_ContextTimeout_ReturnsError(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	// 创建已过期的 context
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond) // 让 context 确实过期

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-jti"

	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)

	// 由于 context 超时，应返回 error
	assert.Error(t, err, "IsBlacklisted should return error when context times out")
	assert.False(t, isBlacklisted, "should return false on context timeout")
}

// TestAccessTokenBlacklistKeyFormat_CorrectKeyGeneration 测试 key 格式为 auth:blacklist:<jti>
func TestAccessTokenBlacklistKeyFormat_CorrectKeyGeneration(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	jti := "abc-def-ghi-123"
	expectedKey := "auth:blacklist:abc-def-ghi-123"
	_ = client.Del(ctx, expectedKey)
	defer func() { _ = client.Del(ctx, expectedKey) }()

	blacklist := NewAccessTokenBlacklist(client)

	// 添加到黑名单
	err := blacklist.Add(ctx, jti, 10*time.Minute)
	require.NoError(t, err)

	// 通过直接访问 Redis 验证 key 格式
	val, err := client.Get(ctx, expectedKey).Result()
	require.NoError(t, err, "key should exist with correct format")
	assert.Equal(t, "1", val, "key value should be '1'")
}

// TestAccessTokenBlacklistAdd_MultipleJTIs_IndependentStorage 测试多个 JTI 独立存储
func TestAccessTokenBlacklistAdd_MultipleJTIs_IndependentStorage(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	jti1 := "jti-1-xyz"
	jti2 := "jti-2-xyz"
	key1 := "auth:blacklist:" + jti1
	key2 := "auth:blacklist:" + jti2
	_ = client.Del(ctx, key1, key2)
	defer func() { _ = client.Del(ctx, key1, key2) }()

	blacklist := NewAccessTokenBlacklist(client)

	// 添加两个不同的 JTI
	err1 := blacklist.Add(ctx, jti1, 10*time.Minute)
	err2 := blacklist.Add(ctx, jti2, 10*time.Minute)
	require.NoError(t, err1)
	require.NoError(t, err2)

	// 验证两个都存在且独立
	is1Blacklisted, err := blacklist.IsBlacklisted(ctx, jti1)
	require.NoError(t, err)
	assert.True(t, is1Blacklisted, "jti1 should be blacklisted")

	is2Blacklisted, err := blacklist.IsBlacklisted(ctx, jti2)
	require.NoError(t, err)
	assert.True(t, is2Blacklisted, "jti2 should be blacklisted")
}

// TestAccessTokenBlacklistIsBlacklisted_FailOpenBehavior_ErrorDoesNotBecomeTrue 测试 fail-open：错误时绝不返回 true
func TestAccessTokenBlacklistIsBlacklisted_FailOpenBehavior_ErrorDoesNotBecomeTrue(t *testing.T) {
	// 创建指向不可达的 Redis
	client := redis.NewClient(&redis.Options{
		Addr:        "10.255.255.1:6379", // 不可达 IP，会超时
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
	})
	defer client.Close()

	ctx := context.Background()
	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-jti-fail-open"

	// Redis 故障时，Fail-open 原则：宁可误 miss（false），不可误 hit（true）
	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)

	// 关键断言：error 不为 nil，但 isBlacklisted 是 false（fail-open）
	require.Error(t, err, "should return error on Redis failure")
	assert.False(t, isBlacklisted, "fail-open: must return false even on Redis error, never true")
}

// TestAccessTokenBlacklistAdd_ShortTTL_MinimalDelay 测试小于 1 秒的 TTL
func TestAccessTokenBlacklistAdd_ShortTTL_MinimalDelay(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:test-short-ttl"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-short-ttl"
	ttl := 100 * time.Millisecond

	err := blacklist.Add(ctx, jti, ttl)
	require.NoError(t, err)

	// 验证存在
	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "key should exist")

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	exists, err = client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key should be evicted")
}

// TestAccessTokenBlacklistAdd_LongTTL_AccessTokenMaxExpiry 测试长 TTL（访问 token 最长有效期）
func TestAccessTokenBlacklistAdd_LongTTL_AccessTokenMaxExpiry(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	key := "auth:blacklist:test-long-ttl"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)
	jti := "test-long-ttl"
	ttl := 1 * time.Hour // 访问 token 可能的最长存活期

	err := blacklist.Add(ctx, jti, ttl)
	require.NoError(t, err)

	// 验证存在
	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err)
	assert.True(t, isBlacklisted, "should be blacklisted with long TTL")
}

// TestAccessTokenBlacklistIsBlacklisted_CaseInsensitiveJTI_DifferentKeysSeparate 测试 JTI 大小写敏感
func TestAccessTokenBlacklistIsBlacklisted_CaseInsensitiveJTI_DifferentKeysSeparate(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	jtiLower := "test-abc-123"
	jtiUpper := "test-ABC-123"
	keyLower := "auth:blacklist:" + jtiLower
	keyUpper := "auth:blacklist:" + jtiUpper
	_ = client.Del(ctx, keyLower, keyUpper)
	defer func() { _ = client.Del(ctx, keyLower, keyUpper) }()

	blacklist := NewAccessTokenBlacklist(client)

	// 只添加小写版本
	_ = blacklist.Add(ctx, jtiLower, 10*time.Minute)

	// 验证小写存在
	is1, err := blacklist.IsBlacklisted(ctx, jtiLower)
	require.NoError(t, err)
	assert.True(t, is1, "lowercase jti should be blacklisted")

	// 验证大写不存在（key 大小写敏感）
	is2, err := blacklist.IsBlacklisted(ctx, jtiUpper)
	require.NoError(t, err)
	assert.False(t, is2, "uppercase jti should NOT be blacklisted (keys are case-sensitive)")
}

// TestAccessTokenBlacklistAdd_SpecialCharactersInJTI_ValidKeyGeneration 测试 JTI 包含特殊字符
func TestAccessTokenBlacklistAdd_SpecialCharactersInJTI_ValidKeyGeneration(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	jti := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ" // Base64 JWT-like string
	key := "auth:blacklist:" + jti
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)

	err := blacklist.Add(ctx, jti, 10*time.Minute)
	require.NoError(t, err, "should handle JTI with special characters")

	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err)
	assert.True(t, isBlacklisted, "should find blacklisted JTI with special characters")
}

// TestAccessTokenBlacklistIsBlacklisted_AfterAdd_ImmediateVisibility 测试 Add 后立即可查询
func TestAccessTokenBlacklistIsBlacklisted_AfterAdd_ImmediateVisibility(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	jti := "test-immediate-visibility"
	key := "auth:blacklist:" + jti
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)

	// 添加到黑名单
	err := blacklist.Add(ctx, jti, 10*time.Minute)
	require.NoError(t, err)

	// 立即查询（无延迟）
	isBlacklisted, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err)
	assert.True(t, isBlacklisted, "should be immediately visible after Add")
}

// TestAccessTokenBlacklistAdd_SameJTITwice_Overwrites 测试同一 JTI 添加两次时覆盖
func TestAccessTokenBlacklistAdd_SameJTITwice_Overwrites(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	jti := "test-overwrite"
	key := "auth:blacklist:" + jti
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	blacklist := NewAccessTokenBlacklist(client)

	// 第一次：10 秒 TTL
	err := blacklist.Add(ctx, jti, 10*time.Second)
	require.NoError(t, err)

	// 验证第一次成功
	is1, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err)
	assert.True(t, is1)

	// 第二次：覆盖为 1 小时 TTL
	err = blacklist.Add(ctx, jti, 1*time.Hour)
	require.NoError(t, err)

	// 验证仍在黑名单
	is2, err := blacklist.IsBlacklisted(ctx, jti)
	require.NoError(t, err)
	assert.True(t, is2, "should still be blacklisted after second Add")

	// 验证新 TTL 已应用（通过 PTTL 检查，应该是 ~1h，不是 ~10s）
	pttl, err := client.PTTL(ctx, key).Result()
	require.NoError(t, err)
	assert.Greater(t, pttl, 50*time.Minute, "new TTL should be applied (close to 1 hour)")
}

// BenchmarkAccessTokenBlacklistAdd_Throughput 性能基准：Add 吞吐量
func BenchmarkAccessTokenBlacklistAdd_Throughput(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis unavailable: %v", err)
	}

	blacklist := NewAccessTokenBlacklist(client)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		jti := fmt.Sprintf("jti-%d", i)
		_ = blacklist.Add(ctx, jti, 10*time.Minute)
	}
}

// BenchmarkAccessTokenBlacklistIsBlacklisted_Throughput 性能基准：IsBlacklisted 吞吐量
func BenchmarkAccessTokenBlacklistIsBlacklisted_Throughput(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis unavailable: %v", err)
	}

	blacklist := NewAccessTokenBlacklist(client)

	// 预加载一个 JTI
	_ = blacklist.Add(ctx, "benchmark-jti", 1*time.Hour)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = blacklist.IsBlacklisted(ctx, "benchmark-jti")
	}
}
