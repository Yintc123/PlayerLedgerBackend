package redis

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserRevocationStoreRevoke_ValidUserIDAndTTL_SetexSucceeds 測試有效 userID 與 TTL 成功寫入 watermark
func TestUserRevocationStoreRevoke_ValidUserIDAndTTL_SetexSucceeds(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-1"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	store := NewUserRevocationStore(client)
	ttl := 30 * 24 * time.Hour

	before := time.Now().Unix()
	err := store.Revoke(ctx, userID, ttl)
	require.NoError(t, err, "Revoke should succeed with valid userID and TTL")
	after := time.Now().Unix()

	// 驗證 key 確實存在
	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "key should exist in Redis")

	// 驗證 value 為 unix seconds（落在呼叫前後區間）
	val, err := client.Get(ctx, key).Result()
	require.NoError(t, err)
	ts, err := strconv.ParseInt(val, 10, 64)
	require.NoError(t, err, "value must be a unix timestamp")
	assert.GreaterOrEqual(t, ts, before)
	assert.LessOrEqual(t, ts, after)

	// 驗證 TTL 被正確套用
	pttl, err := client.PTTL(ctx, key).Result()
	require.NoError(t, err)
	assert.Greater(t, pttl, 29*24*time.Hour, "TTL should be close to 30 days")
}

// TestUserRevocationStoreRevoke_ZeroTTL_NoOp 測試 ttl≤0 時不寫入
func TestUserRevocationStoreRevoke_ZeroTTL_NoOp(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-zero-ttl"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	store := NewUserRevocationStore(client)

	err := store.Revoke(ctx, userID, 0)
	require.NoError(t, err, "Revoke with TTL=0 should return no error (no-op)")

	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key should not exist for TTL=0 (no-op)")
}

// TestUserRevocationStoreRevoke_NegativeTTL_NoOp 測試負 TTL 不寫入
func TestUserRevocationStoreRevoke_NegativeTTL_NoOp(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-neg-ttl"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	store := NewUserRevocationStore(client)

	err := store.Revoke(ctx, userID, -1*time.Minute)
	require.NoError(t, err, "Revoke with negative TTL should return no error (no-op)")

	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key should not exist for negative TTL (no-op)")
}

// TestUserRevocationStoreRevokedAfter_KeyNotExists_ReturnsZeroNil 測試從未被 revoke 時回 (0, nil)
func TestUserRevocationStoreRevokedAfter_KeyNotExists_ReturnsZeroNil(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-missing"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)

	store := NewUserRevocationStore(client)

	ts, err := store.RevokedAfter(ctx, userID)
	require.NoError(t, err, "RevokedAfter must NOT return error for missing key (means 'never revoked')")
	assert.Equal(t, int64(0), ts, "missing key should return (0, nil)")
}

// TestUserRevocationStoreRevokedAfter_KeyExists_ReturnsWatermark 測試已 revoke 時回 watermark
func TestUserRevocationStoreRevokedAfter_KeyExists_ReturnsWatermark(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-existing"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	store := NewUserRevocationStore(client)

	before := time.Now().Unix()
	require.NoError(t, store.Revoke(ctx, userID, 30*24*time.Hour))
	after := time.Now().Unix()

	ts, err := store.RevokedAfter(ctx, userID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ts, before)
	assert.LessOrEqual(t, ts, after)
}

// TestUserRevocationStoreRevokedAfter_RedisFailure_ReturnsZeroErr 測試 Redis 故障時回 (0, err)：caller fail-open
func TestUserRevocationStoreRevokedAfter_RedisFailure_ReturnsZeroErr(t *testing.T) {
	// 建立指向不存在埠的 client，模擬連線故障
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:55556",
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
	})
	defer client.Close()

	ctx := context.Background()
	store := NewUserRevocationStore(client)

	ts, err := store.RevokedAfter(ctx, "any-user")

	require.Error(t, err, "RevokedAfter must return error when Redis is unavailable")
	assert.Equal(t, int64(0), ts, "fail-open: ts must be 0 on Redis error, never a stale value")
}

// TestUserRevocationStoreKeyFormat_HashTagBraces 測試 key 格式包含 {<userID>} hash tag
func TestUserRevocationStoreKeyFormat_HashTagBraces(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "abc-123"
	expectedKey := "auth:user_revoked_after:{abc-123}"
	_ = client.Del(ctx, expectedKey)
	defer func() { _ = client.Del(ctx, expectedKey) }()

	store := NewUserRevocationStore(client)
	require.NoError(t, store.Revoke(ctx, userID, 30*24*time.Hour))

	// 直接以期望 key 讀取（驗證 hash tag 格式）
	val, err := client.Get(ctx, expectedKey).Result()
	require.NoError(t, err, "key must use hash-tag format auth:user_revoked_after:{<userID>}")
	_, err = strconv.ParseInt(val, 10, 64)
	require.NoError(t, err, "value must be a unix timestamp")
}

// TestUserRevocationStoreRevoke_OverwritesWatermark 測試重複 Revoke 會覆寫先前的 watermark
func TestUserRevocationStoreRevoke_OverwritesWatermark(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-overwrite"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	store := NewUserRevocationStore(client)

	require.NoError(t, store.Revoke(ctx, userID, 30*24*time.Hour))
	first, err := store.RevokedAfter(ctx, userID)
	require.NoError(t, err)

	// 等 1 秒保證 unix ts 推進
	time.Sleep(1100 * time.Millisecond)

	require.NoError(t, store.Revoke(ctx, userID, 30*24*time.Hour))
	second, err := store.RevokedAfter(ctx, userID)
	require.NoError(t, err)

	assert.Greater(t, second, first, "second Revoke should overwrite watermark with newer unix ts")
}

// TestUserRevocationStoreRevoke_TTLExpiry_KeyEvicted 測試 TTL 到期後 key 自動清理
func TestUserRevocationStoreRevoke_TTLExpiry_KeyEvicted(t *testing.T) {
	client := newTestRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	userID := "user-revoke-expire"
	key := "auth:user_revoked_after:{" + userID + "}"
	_ = client.Del(ctx, key)
	defer func() { _ = client.Del(ctx, key) }()

	store := NewUserRevocationStore(client)
	require.NoError(t, store.Revoke(ctx, userID, 500*time.Millisecond))

	exists, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "key should exist immediately after Revoke")

	time.Sleep(700 * time.Millisecond)

	ts, err := store.RevokedAfter(ctx, userID)
	require.NoError(t, err, "expired key behaves like missing key — (0, nil)")
	assert.Equal(t, int64(0), ts, "after TTL expiry, watermark should be (0, nil)")
}
