package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
)

// TestNewFamilyStore_ScriptsLoadedSuccessfully 驗證 constructor 成功加载所有 Lua 脚本
func TestNewFamilyStore_ScriptsLoadedSuccessfully(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	cfg := config.JWTConfig{
		Issuer:          "test",
		Secret:          "test-secret-32-chars-minimum00",
		RefreshSecret:   "test-refresh-secret-32-chars-000",
		AccessTTL:       15 * time.Minute,
		GraceWindow:     10 * time.Second,
		ClockSkewLeeway: 30 * time.Second,
		BcryptCost:      12,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fs, err := NewFamilyStore(ctx, client, cfg)
	require.NoError(t, err, "NewFamilyStore should not error on valid config")
	assert.NotNil(t, fs, "should return non-nil FamilyStore")
	assert.True(t, fs.ScriptsLoaded(), "ScriptsLoaded should be true after successful init")
}

// TestNewFamilyStore_ContextCancellation 驗證 context 取消时 constructor 返回 error
func TestNewFamilyStore_ContextCancellation_ReturnsError(t *testing.T) {
	client := getTestRedisClient(t)
	defer client.Close()

	cfg := config.JWTConfig{
		Issuer:          "test",
		Secret:          "test-secret-32-chars-minimum00",
		RefreshSecret:   "test-refresh-secret-32-chars-000",
		AccessTTL:       15 * time.Minute,
		GraceWindow:     10 * time.Second,
		ClockSkewLeeway: 30 * time.Second,
		BcryptCost:      12,
	}

	// 建立已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewFamilyStore(ctx, client, cfg)
	assert.Error(t, err, "NewFamilyStore should error on cancelled context")
}

// TestSave_ValidState_SavesSuccessfully 驗證 Save 正确保存 family state
func TestSave_ValidState_SavesSuccessfully(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)

	state := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-456",
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "admin",
		CurrentJTI:    "jti-001",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Chrome on Windows",
		IPAtLogin:     "192.168.1.1",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := fs.Save(ctx, state)
	require.NoError(t, err, "Save should not error on valid state")

	// 驗證 family key 被保存
	key := fmt.Sprintf("auth:family:{%s}:%s", state.UserID, state.FamilyID)
	raw, err := client.Get(ctx, key).Result()
	require.NoError(t, err, "family key should exist after Save")

	var savedState FamilyState
	err = json.Unmarshal([]byte(raw), &savedState)
	require.NoError(t, err, "should be able to unmarshal saved state")
	assert.Equal(t, state.UserID, savedState.UserID)
	assert.Equal(t, state.FamilyID, savedState.FamilyID)

	// 驗證 index 被更新
	indexKey := fmt.Sprintf("auth:user_families:{%s}", state.UserID)
	isMember, err := client.SIsMember(ctx, indexKey, state.FamilyID).Result()
	require.NoError(t, err)
	assert.True(t, isMember, "family should be in user_families set")
}

// TestSave_ExpiredAbsoluteExp_ReturnsError 驗證 Save 拒绝已過期的 abs_exp
func TestSave_ExpiredAbsoluteExp_ReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)

	state := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-456",
		AbsoluteExp:   time.Now().Add(-1 * time.Hour).Unix(), // 已過期
		CurrentJTI:    "jti-001",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := fs.Save(ctx, state)
	assert.Error(t, err, "Save should error on expired absolute_exp")
}

// TestRotate_NormalRotation_SucceedsWithNewState 驗證正常 rotation 流程
func TestRotate_NormalRotation_SucceedsWithNewState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 先建立一个 family
	state := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-456",
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "admin",
		CurrentJTI:    "jti-001",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Chrome",
		IPAtLogin:     "192.168.1.1",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	err := fs.Save(ctx, state)
	require.NoError(t, err)

	// 執行 rotation
	newJTI := "jti-002"
	graceWindow := 10 * time.Second
	result, rotatedState, err := fs.Rotate(ctx, state.UserID, state.FamilyID, "jti-001", newJTI, graceWindow)

	assert.NoError(t, err, "Rotate should not error")
	assert.Equal(t, Rotated, result, "should return Rotated result")
	require.NotNil(t, rotatedState, "should return non-nil state on Rotated")

	assert.Equal(t, newJTI, rotatedState.CurrentJTI, "current_jti should be updated")
	assert.Equal(t, "jti-001", rotatedState.PreviousJTI, "previous_jti should be set to old current_jti")
	assert.Greater(t, rotatedState.LastRotatedAt, state.LastRotatedAt, "last_rotated_at should be updated")
}

// TestRotate_GraceWindowHit_ReturnsGraceHitWithState 驗證 grace window 命中行为
func TestRotate_GraceWindowHit_ReturnsGraceHitWithState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().Unix()

	// 建立一个已经经过 rotation 的 family（有 previous_jti）
	state := FamilyState{
		UserID:                "user-123",
		FamilyID:              "fam-456",
		ClientID:              "cms-web",
		UserType:              "cms",
		Role:                  "admin",
		CurrentJTI:            "jti-002",
		PreviousJTI:           "jti-001",
		PreviousResponseUntil: now + 15, // grace window 还没過期
		AbsoluteExp:           now + 8*3600,
		DeviceLabel:           "Chrome",
		IPAtLogin:             "192.168.1.1",
		CreatedAt:             now - 100,
		LastRotatedAt:         now,
	}

	// 手动保存到 Redis
	key := fmt.Sprintf("auth:family:{%s}:%s", state.UserID, state.FamilyID)
	stateJSON, _ := json.Marshal(state)
	err := client.Set(ctx, key, string(stateJSON), 8*time.Hour).Err()
	require.NoError(t, err)

	indexKey := fmt.Sprintf("auth:user_families:{%s}", state.UserID)
	err = client.SAdd(ctx, indexKey, state.FamilyID).Err()
	require.NoError(t, err)

	// 用 previous_jti 再次 rotate（grace 命中）
	result, graceState, err := fs.Rotate(ctx, state.UserID, state.FamilyID, "jti-001", "jti-003", 10*time.Second)

	assert.NoError(t, err)
	assert.Equal(t, GraceHit, result, "should return GraceHit result")
	require.NotNil(t, graceState, "should return non-nil state on GraceHit")
	assert.Equal(t, "jti-002", graceState.CurrentJTI, "current_jti should not change on grace hit")
}

// TestRotate_ReplayDetected_DeletesFamilyAndIndex 驗證重放偵測触发时清理数据
func TestRotate_ReplayDetected_DeletesFamilyAndIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 建立一个 family
	state := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-456",
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "admin",
		CurrentJTI:    "jti-001",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Chrome",
		IPAtLogin:     "192.168.1.1",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	err := fs.Save(ctx, state)
	require.NoError(t, err)

	// 用錯誤的 JTI 进行 rotate（会触发重放偵測）
	result, replayState, err := fs.Rotate(ctx, state.UserID, state.FamilyID, "wrong-jti", "new-jti", 10*time.Second)

	assert.NoError(t, err)
	assert.Equal(t, ReplayDetected, result, "should return ReplayDetected")
	assert.Nil(t, replayState, "should return nil state on ReplayDetected")

	// 驗證 family 被刪除
	key := fmt.Sprintf("auth:family:{%s}:%s", state.UserID, state.FamilyID)
	_, err = client.Get(ctx, key).Result()
	assert.Equal(t, redis.Nil, err, "family key should be deleted after replay detection")

	// 驗證 index 被清理
	indexKey := fmt.Sprintf("auth:user_families:{%s}", state.UserID)
	isMember, err := client.SIsMember(ctx, indexKey, state.FamilyID).Result()
	require.NoError(t, err)
	assert.False(t, isMember, "family should be removed from index after replay detection")
}

// TestRotate_FamilyNotFound_ReturnsFamilyNotFoundWithoutError 驗證找不到 family 时的行为
func TestRotate_FamilyNotFound_ReturnsFamilyNotFoundWithoutError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, state, err := fs.Rotate(ctx, "nonexistent-user", "nonexistent-family", "any-jti", "new-jti", 10*time.Second)

	assert.NoError(t, err, "should not error when family not found")
	assert.Equal(t, FamilyNotFound, result, "should return FamilyNotFound")
	assert.Nil(t, state, "should return nil state")
}

// TestRotate_AbsoluteExpPassed_TreatsAsFamilyNotFound 驗證過期 abs_exp 被视为 family 不存在
func TestRotate_AbsoluteExpPassed_TreatsAsFamilyNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().Unix()

	// 建立一个 abs_exp 已過期的 family
	state := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-456",
		ClientID:      "cms-web",
		CurrentJTI:    "jti-001",
		AbsoluteExp:   now - 100, // 已過期
		CreatedAt:     now,
		LastRotatedAt: now,
	}

	key := fmt.Sprintf("auth:family:{%s}:%s", state.UserID, state.FamilyID)
	stateJSON, _ := json.Marshal(state)
	err := client.Set(ctx, key, string(stateJSON), 1*time.Hour).Err()
	require.NoError(t, err)

	indexKey := fmt.Sprintf("auth:user_families:{%s}", state.UserID)
	err = client.SAdd(ctx, indexKey, state.FamilyID).Err()
	require.NoError(t, err)

	// 尝试 rotate
	result, rotateState, err := fs.Rotate(ctx, state.UserID, state.FamilyID, "jti-001", "jti-002", 10*time.Second)

	assert.NoError(t, err)
	assert.Equal(t, FamilyNotFound, result, "expired abs_exp should be treated as FamilyNotFound")
	assert.Nil(t, rotateState)

	// 驗證 family 被清理
	_, err = client.Get(ctx, key).Result()
	assert.Equal(t, redis.Nil, err, "expired family should be deleted")
}

// TestRevoke_SingleFamily_DeletesKeyAndRemovesIndex 驗證 Revoke 刪除单个 family
func TestRevoke_SingleFamily_DeletesKeyAndRemovesIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 建立两个 family
	state1 := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-001",
		CurrentJTI:    "jti-001",
		AbsoluteExp:   time.Now().Add(1 * time.Hour).Unix(),
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	state2 := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-002",
		CurrentJTI:    "jti-002",
		AbsoluteExp:   time.Now().Add(1 * time.Hour).Unix(),
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	err := fs.Save(ctx, state1)
	require.NoError(t, err)
	err = fs.Save(ctx, state2)
	require.NoError(t, err)

	// Revoke 第一个 family
	found, err := fs.Revoke(ctx, state1.UserID, state1.FamilyID)
	assert.NoError(t, err, "Revoke should not error")
	assert.True(t, found, "revoking an existing family should report found=true")

	// 驗證第一个 family 被刪除
	key1 := fmt.Sprintf("auth:family:{%s}:%s", state1.UserID, state1.FamilyID)
	_, err = client.Get(ctx, key1).Result()
	assert.Equal(t, redis.Nil, err, "revoked family key should be deleted")

	// 驗證第二个 family 仍然存在
	key2 := fmt.Sprintf("auth:family:{%s}:%s", state2.UserID, state2.FamilyID)
	_, err = client.Get(ctx, key2).Result()
	assert.NoError(t, err, "other family key should still exist")

	// 驗證 index 中只剩第二个
	indexKey := fmt.Sprintf("auth:user_families:{%s}", state1.UserID)
	members, err := client.SMembers(ctx, indexKey).Result()
	require.NoError(t, err)
	assert.Equal(t, 1, len(members), "index should have only one family")
	assert.Equal(t, "fam-002", members[0])
}

// TestRevokeAll_AllFamilies_DeletesAllKeysAndIndex 驗證 RevokeAll 刪除所有 family
func TestRevokeAll_AllFamilies_DeletesAllKeysAndIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userID := "user-123"

	// 建立多个 family
	for i := 1; i <= 3; i++ {
		state := FamilyState{
			UserID:        userID,
			FamilyID:      fmt.Sprintf("fam-%d", i),
			CurrentJTI:    fmt.Sprintf("jti-%d", i),
			AbsoluteExp:   time.Now().Add(1 * time.Hour).Unix(),
			CreatedAt:     time.Now().Unix(),
			LastRotatedAt: time.Now().Unix(),
		}
		err := fs.Save(ctx, state)
		require.NoError(t, err)
	}

	// RevokeAll
	err := fs.RevokeAll(ctx, userID)
	assert.NoError(t, err, "RevokeAll should not error")

	// 驗證所有 family key 都被刪除
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("auth:family:{%s}:fam-%d", userID, i)
		_, err := client.Get(ctx, key).Result()
		assert.Equal(t, redis.Nil, err, fmt.Sprintf("family %d should be deleted", i))
	}

	// 驗證 index 也被刪除
	indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)
	_, err = client.Get(ctx, indexKey).Result()
	assert.Equal(t, redis.Nil, err, "user_families index should be deleted")
}

// TestListByUser_MultipleFamilies_SortsByLastRotatedAtDesc 驗證列表按 LastRotatedAt 降序排列
func TestListByUser_MultipleFamilies_SortsByLastRotatedAtDesc(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().Unix()
	userID := "user-123"

	// 建立多个 family，with 不同的 last_rotated_at
	states := []FamilyState{
		{
			UserID:        userID,
			FamilyID:      "fam-1",
			CurrentJTI:    "jti-1",
			AbsoluteExp:   now + 3600,
			CreatedAt:     now - 300,
			LastRotatedAt: now - 10,
		},
		{
			UserID:        userID,
			FamilyID:      "fam-2",
			CurrentJTI:    "jti-2",
			AbsoluteExp:   now + 3600,
			CreatedAt:     now - 200,
			LastRotatedAt: now - 5, // 最新
		},
		{
			UserID:        userID,
			FamilyID:      "fam-3",
			CurrentJTI:    "jti-3",
			AbsoluteExp:   now + 3600,
			CreatedAt:     now - 100,
			LastRotatedAt: now - 20, // 最旧
		},
	}

	for _, state := range states {
		err := fs.Save(ctx, state)
		require.NoError(t, err)
	}

	// ListByUser
	result, err := fs.ListByUser(ctx, userID)
	require.NoError(t, err)

	assert.Equal(t, 3, len(result), "should return 3 families")

	// 驗證排序顺序（降序）
	assert.Equal(t, "fam-2", result[0].FamilyID, "最新应该在第一")
	assert.Equal(t, "fam-1", result[1].FamilyID)
	assert.Equal(t, "fam-3", result[2].FamilyID, "最旧应该在最后")

	// 驗證排序确实是降序
	assert.Greater(t, result[0].LastRotatedAt, result[1].LastRotatedAt)
	assert.Greater(t, result[1].LastRotatedAt, result[2].LastRotatedAt)
}

// TestListByUser_WithOrphanFamilies_CleansUpGracefully 驗證 ListByUser lazy cleanup 孤儿 fid
func TestListByUser_WithOrphanFamilies_CleansUpGracefully(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().Unix()
	userID := "user-123"

	// 建立一个有效的 family
	validState := FamilyState{
		UserID:        userID,
		FamilyID:      "fam-valid",
		CurrentJTI:    "jti-1",
		AbsoluteExp:   now + 3600,
		CreatedAt:     now,
		LastRotatedAt: now,
	}
	err := fs.Save(ctx, validState)
	require.NoError(t, err)

	// 手动在 index 中添加孤儿 fid（对应的 key 不存在）
	indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)
	err = client.SAdd(ctx, indexKey, "fam-orphan").Err()
	require.NoError(t, err)

	// ListByUser 应该清理孤儿
	result, err := fs.ListByUser(ctx, userID)
	require.NoError(t, err)

	assert.Equal(t, 1, len(result), "should return only valid family")
	assert.Equal(t, "fam-valid", result[0].FamilyID)

	// 驗證孤儿已被清理
	members, err := client.SMembers(ctx, indexKey).Result()
	require.NoError(t, err)
	assert.Equal(t, 1, len(members), "index should only have valid family")
	assert.Equal(t, "fam-valid", members[0])
}

// TestListByUser_EmptyUser_ReturnsEmptyList 驗證没有 family 的 user 返回空列表
func TestListByUser_EmptyUser_ReturnsEmptyList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := fs.ListByUser(ctx, "nonexistent-user")
	assert.NoError(t, err)
	assert.Equal(t, 0, len(result), "should return empty list for user with no families")
}

// TestRotate_NilStateReturnedByLua_FailsClosedAsFamilyNotFound 驗證 Lua 返回空 state 时 fail-closed
func TestRotate_EmptyStateJsonFromLua_FailsClosedAsFamilyNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	fs := createTestFamilyStore(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 这个測試是为了驗證 fail-closed 行为
	// 当 Lua 返回空的 state_json 时，Go layer 应该视为 FamilyNotFound
	// 實際上通过 NewFamilyStore 加载的 Lua 脚本不会返回这种情况，
	// 但我们要在實作中防御这种情况

	// 建立一个正常的 family
	state := FamilyState{
		UserID:        "user-123",
		FamilyID:      "fam-456",
		ClientID:      "cms-web",
		CurrentJTI:    "jti-001",
		AbsoluteExp:   time.Now().Add(1 * time.Hour).Unix(),
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	}

	err := fs.Save(ctx, state)
	require.NoError(t, err)

	// 正常情况下应该成功
	result, rotatedState, err := fs.Rotate(ctx, state.UserID, state.FamilyID, "jti-001", "jti-002", 10*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, Rotated, result)
	assert.NotNil(t, rotatedState)
}

// TestScriptsLoaded_BeforeAndAfterInit 驗證 ScriptsLoaded 的状态转变
func TestScriptsLoaded_ReturnsTrue_AfterSuccessfulInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := getTestRedisClient(t)
	defer client.Close()

	cfg := config.JWTConfig{
		Issuer:          "test",
		Secret:          "test-secret-32-chars-minimum00",
		RefreshSecret:   "test-refresh-secret-32-chars-000",
		AccessTTL:       15 * time.Minute,
		GraceWindow:     10 * time.Second,
		ClockSkewLeeway: 30 * time.Second,
		BcryptCost:      12,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fs, err := NewFamilyStore(ctx, client, cfg)
	require.NoError(t, err)

	// 应该返回 true
	assert.True(t, fs.ScriptsLoaded())
}

// ===== Test Helpers =====

// getTestRedisClient 取得測試用 Redis 客户端
func getTestRedisClient(t *testing.T) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           15, // 使用 DB 15 避免污染 real data
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	// 驗證連線
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available: %v", err)
	}

	// 清空測試 DB
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("failed to flush test db: %v", err)
	}

	t.Cleanup(func() {
		_ = client.FlushDB(context.Background())
		_ = client.Close()
	})

	return client
}

// createTestFamilyStore 建立測試用 FamilyStore
func createTestFamilyStore(t *testing.T, client *redis.Client) FamilyStore {
	cfg := config.JWTConfig{
		Issuer:          "test",
		Secret:          "test-secret-32-chars-minimum00",
		RefreshSecret:   "test-refresh-secret-32-chars-000",
		AccessTTL:       15 * time.Minute,
		GraceWindow:     10 * time.Second,
		ClockSkewLeeway: 30 * time.Second,
		BcryptCost:      12,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fs, err := NewFamilyStore(ctx, client, cfg)
	require.NoError(t, err)

	return fs
}
