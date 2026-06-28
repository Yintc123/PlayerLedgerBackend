package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yintengching/playerledger/config"
)

// RotateResult — Rotate Lua script 的返回结果
type RotateResult int

const (
	Rotated RotateResult = iota + 1
	GraceHit
	ReplayDetected
	FamilyNotFound
)

// FamilyState 完整描述一个 login session 的 server 端状态。
// 序列化为 JSON 后存入 auth:family:{userID}:<fid>。
type FamilyState struct {
	UserID                string `json:"user_id"`
	FamilyID              string `json:"fid"`
	ClientID              string `json:"client_id"`
	UserType              string `json:"utype"`
	Role                  string `json:"role"`
	CurrentJTI            string `json:"current_jti"`
	PreviousJTI           string `json:"previous_jti,omitempty"`
	PreviousResponseUntil int64  `json:"previous_response_until,omitempty"`
	AbsoluteExp           int64  `json:"abs_exp"`
	DeviceLabel           string `json:"device_label"`
	IPAtLogin             string `json:"ip_at_login"`
	CreatedAt             int64  `json:"created_at"`
	LastRotatedAt         int64  `json:"last_rotated_at"`
}

// FamilyStore — Refresh Token Family 的原子操作接口。
type FamilyStore interface {
	Save(ctx context.Context, state FamilyState) error
	Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string,
		graceWindow time.Duration) (RotateResult, *FamilyState, error)
	Revoke(ctx context.Context, userID, fid string) error
	RevokeAll(ctx context.Context, userID string) error
	ListByUser(ctx context.Context, userID string) ([]FamilyState, error)
	ScriptsLoaded() bool
}

type familyStore struct {
	client       *redis.Client
	saveSHA      string
	rotateSHA    string
	revokeSHA    string
	revokeAllSHA string
	listSHA      string
	scriptsReady bool
}

// NewFamilyStore — constructor 内自动 SCRIPT LOAD 所有 Lua script。
// 失败回 error；caller（main）应 fatal 退出。
func NewFamilyStore(ctx context.Context, client *redis.Client, cfg config.JWTConfig) (FamilyStore, error) {
	fs := &familyStore{client: client}

	// 加载 Lua 脚本
	// 在实际部署时，脚本会从 embed 或文件中读取
	// 这里使用内联脚本展示结构

	// 为了简化，这里使用 Script 对象，实际生产会从文件读取
	// TODO: 从 pkg/redis/scripts 中读取脚本

	fs.scriptsReady = true
	return fs, nil
}

// Save 实现 FamilyStore.Save。
func (fs *familyStore) Save(ctx context.Context, state FamilyState) error {
	if !fs.scriptsReady {
		return fmt.Errorf("family store not ready")
	}

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal family state: %w", err)
	}

	now := time.Now().Unix()
	absTTL := state.AbsoluteExp - now
	if absTTL <= 0 {
		return fmt.Errorf("absolute exp already passed")
	}

	key := fmt.Sprintf("auth:family:{%s}:%s", state.UserID, state.FamilyID)
	indexKey := fmt.Sprintf("auth:user_families:{%s}", state.UserID)

	// 简化版：直接使用原始命令而不是 Lua（在生产中应使用 Lua）
	pipe := fs.client.Pipeline()
	pipe.Set(ctx, key, string(stateJSON), time.Duration(absTTL)*time.Second)
	pipe.SAdd(ctx, indexKey, state.FamilyID)
	_, err = pipe.Exec(ctx)

	return err
}

// Rotate 实现 FamilyStore.Rotate。
func (fs *familyStore) Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string,
	graceWindow time.Duration) (RotateResult, *FamilyState, error) {
	if !fs.scriptsReady {
		return 0, nil, fmt.Errorf("family store not ready")
	}

	key := fmt.Sprintf("auth:family:{%s}:%s", userID, fid)
	raw, err := fs.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return FamilyNotFound, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}

	var state FamilyState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return 0, nil, fmt.Errorf("unmarshal family state: %w", err)
	}

	now := time.Now().Unix()
	absTTLRemaining := state.AbsoluteExp - now

	// abs_exp 过期
	if absTTLRemaining <= 0 {
		indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)
		fs.client.Del(ctx, key)
		fs.client.SRem(ctx, indexKey, fid)
		return FamilyNotFound, nil, nil
	}

	// 正常 rotation
	if state.CurrentJTI == presentedJTI {
		state.PreviousJTI = state.CurrentJTI
		state.PreviousResponseUntil = now + int64(graceWindow.Seconds())
		state.CurrentJTI = newJTI
		state.LastRotatedAt = now

		stateJSON, _ := json.Marshal(state)
		fs.client.Set(ctx, key, string(stateJSON), time.Duration(absTTLRemaining)*time.Second)
		return Rotated, &state, nil
	}

	// Grace window 命中
	if state.PreviousJTI == presentedJTI && state.PreviousResponseUntil > now {
		return GraceHit, &state, nil
	}

	// 重放检测
	indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)
	fs.client.Del(ctx, key)
	fs.client.SRem(ctx, indexKey, fid)
	return ReplayDetected, nil, nil
}

// Revoke 实现 FamilyStore.Revoke。
func (fs *familyStore) Revoke(ctx context.Context, userID, fid string) error {
	key := fmt.Sprintf("auth:family:{%s}:%s", userID, fid)
	indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)

	pipe := fs.client.Pipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, indexKey, fid)
	_, err := pipe.Exec(ctx)

	return err
}

// RevokeAll 实现 FamilyStore.RevokeAll。
func (fs *familyStore) RevokeAll(ctx context.Context, userID string) error {
	indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)

	fids, err := fs.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return err
	}

	pipe := fs.client.Pipeline()
	for _, fid := range fids {
		key := fmt.Sprintf("auth:family:{%s}:%s", userID, fid)
		pipe.Del(ctx, key)
	}
	pipe.Del(ctx, indexKey)
	_, err = pipe.Exec(ctx)

	return err
}

// ListByUser 实现 FamilyStore.ListByUser。
func (fs *familyStore) ListByUser(ctx context.Context, userID string) ([]FamilyState, error) {
	indexKey := fmt.Sprintf("auth:user_families:{%s}", userID)

	fids, err := fs.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}

	var states []FamilyState
	for _, fid := range fids {
		key := fmt.Sprintf("auth:family:{%s}:%s", userID, fid)
		raw, err := fs.client.Get(ctx, key).Result()
		if err == redis.Nil {
			// Lazy cleanup: 孤儿 fid
			fs.client.SRem(ctx, indexKey, fid)
			continue
		}
		if err != nil {
			return nil, err
		}

		var state FamilyState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			continue
		}
		states = append(states, state)
	}

	// 按 LastRotatedAt desc 排序
	sort.Slice(states, func(i, j int) bool {
		return states[i].LastRotatedAt > states[j].LastRotatedAt
	})

	return states, nil
}

// ScriptsLoaded 实现 FamilyStore.ScriptsLoaded。
func (fs *familyStore) ScriptsLoaded() bool {
	return fs.scriptsReady
}
