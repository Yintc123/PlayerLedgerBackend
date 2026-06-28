package redis

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yintengching/playerledger/config"
)

//go:embed scripts/*.lua
var scriptsFS embed.FS

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
//
// AbsoluteExp 是 server 信任的 abs_exp 来源：rotation / grace 重签 refresh token
// 时必须从这里取，不能信任 client presented JWT，避免攻击者改 token 内容延长 session。
// Lua 也用此值计算 Redis key TTL，无需 caller 额外传入。
//
// UserType / Role 在 login 时随 family 一起写入，rotation 与 GraceHit 重签
// access token 时直接读 state，不必再打 DB（hot path 维持 stateless）。
type FamilyState struct {
	UserID                string `json:"user_id"`
	FamilyID              string `json:"fid"`
	ClientID              string `json:"client_id"` // = aud claim
	UserType              string `json:"utype"`     // login 时固化；GraceHit / Rotated 重签 access 用
	Role                  string `json:"role"`      // login 时固化；同上
	CurrentJTI            string `json:"current_jti"`
	PreviousJTI           string `json:"previous_jti,omitempty"`            // grace window 用
	PreviousResponseUntil int64  `json:"previous_response_until,omitempty"` // unix seconds；grace 截止
	AbsoluteExp           int64  `json:"abs_exp"`                           // unix seconds；rotation 不延长
	DeviceLabel           string `json:"device_label"`                      // 从 User-Agent 解析
	IPAtLogin             string `json:"ip_at_login"`
	CreatedAt             int64  `json:"created_at"`      // unix seconds
	LastRotatedAt         int64  `json:"last_rotated_at"` // unix seconds
}

// FamilyStore — Refresh Token Family 的原子操作接口。
// Save / Rotate / Revoke / RevokeAll 涉及多 key，皆以 Lua script 一次原子執行。
// ListByUser 采 lazy cleanup：讀取时顺手 SREM 已過期的孤儿 fid。
type FamilyStore interface {
	// Save：login 时建立新 family（同时 SADD 入 user_families 索引）
	Save(ctx context.Context, state FamilyState) error

	// Rotate：原子 CAS — 驗證 presented_jti、更新 current/previous、设定 grace window。
	// Lua 从 state 内部读 AbsoluteExp 计算 Redis TTL；触发重放时 Lua 自动 DEL family + SREM 索引。
	//
	// 回传 invariant：
	//   - Rotated         → (Rotated, *FamilyState non-nil, nil)
	//   - GraceHit        → (GraceHit, *FamilyState non-nil, nil)
	//   - ReplayDetected  → (ReplayDetected, nil, nil)
	//   - FamilyNotFound  → (FamilyNotFound, nil, nil)
	//   - 其他 Redis error → (0, nil, err)
	// 若 Rotated / GraceHit 回传 nil state（Lua bug / state_json 为空），caller **必须** fail-closed
	// 视为 ErrFamilyNotFound，禁止对 nil 解参考造成 panic。
	Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string,
		graceWindow time.Duration) (RotateResult, *FamilyState, error)

	// Revoke：登出单一 family
	Revoke(ctx context.Context, userID, fid string) error

	// RevokeAll：登出该 user 所有 family（改密码 / 强制全装置登出）
	RevokeAll(ctx context.Context, userID string) error

	// ListByUser：列出该 user 所有 family（含 lazy cleanup 孤儿 fid）。
	// 用于 GET /auth/sessions 页面。
	//
	// Redis SMEMBERS 出来无序；本實作须在 Go layer 对结果 **sort by LastRotatedAt desc**，
	// 让「最近活躃的装置」排在前面（UX 预期）。caller 不需再 sort。
	ListByUser(ctx context.Context, userID string) ([]FamilyState, error)

	// ScriptsLoaded 回报 NewFamilyStore constructor 内的 SCRIPT LOAD 是否已成功完成，
	// 供 /health/ready 探测。constructor 内载入成功前回 false；成功后恒为 true
	// （process 生命周期内不重置）。
	//
	// 为何不公开 PreloadScripts：避免 caller 在 constructor 之外 lazy-load 而忘了檢查
	// 回传 error；NewFamilyStore 拿到的 instance 必为「已 ready」状态，否则 constructor
	// 直接回 error，由 main fatal 退出，避免冷启动首次 refresh 才踩到 NOSCRIPT 重试 latency。
	ScriptsLoaded() bool
}

type familyStore struct {
	client       *redis.Client
	saveScript   *redis.Script
	rotateScript *redis.Script
	revokeScript *redis.Script
	revokeAllScr *redis.Script
	listScript   *redis.Script
	scriptsReady bool
}

// NewFamilyStore — constructor 内自动 SCRIPT LOAD 所有 Lua script。
// 失败回 error；caller（main）应 fatal 退出。
// ctx 用于 SCRIPT LOAD 的 timeout / cancel，建议从 main 传入带 timeout 的 ctx
// （例如 5s）避免 Redis hang 卡住启动。
func NewFamilyStore(ctx context.Context, client *redis.Client, cfg config.JWTConfig) (FamilyStore, error) {
	fs := &familyStore{client: client}

	// 从 embed.FS 讀取所有 Lua 脚本
	saveBody, err := scriptsFS.ReadFile("scripts/save.lua")
	if err != nil {
		return nil, fmt.Errorf("read save.lua: %w", err)
	}

	rotateBody, err := scriptsFS.ReadFile("scripts/rotate.lua")
	if err != nil {
		return nil, fmt.Errorf("read rotate.lua: %w", err)
	}

	revokeBody, err := scriptsFS.ReadFile("scripts/revoke.lua")
	if err != nil {
		return nil, fmt.Errorf("read revoke.lua: %w", err)
	}

	revokeAllBody, err := scriptsFS.ReadFile("scripts/revoke_all.lua")
	if err != nil {
		return nil, fmt.Errorf("read revoke_all.lua: %w", err)
	}

	listBody, err := scriptsFS.ReadFile("scripts/list_with_cleanup.lua")
	if err != nil {
		return nil, fmt.Errorf("read list_with_cleanup.lua: %w", err)
	}

	// 包裝成 redis.Script 对象，并自动 SCRIPT LOAD
	fs.saveScript = redis.NewScript(string(saveBody))
	fs.rotateScript = redis.NewScript(string(rotateBody))
	fs.revokeScript = redis.NewScript(string(revokeBody))
	fs.revokeAllScr = redis.NewScript(string(revokeAllBody))
	fs.listScript = redis.NewScript(string(listBody))

	// 预加载所有脚本，失败则 constructor 回 error
	_, err = fs.saveScript.Load(ctx, client).Result()
	if err != nil {
		return nil, fmt.Errorf("load save.lua: %w", err)
	}

	_, err = fs.rotateScript.Load(ctx, client).Result()
	if err != nil {
		return nil, fmt.Errorf("load rotate.lua: %w", err)
	}

	_, err = fs.revokeScript.Load(ctx, client).Result()
	if err != nil {
		return nil, fmt.Errorf("load revoke.lua: %w", err)
	}

	_, err = fs.revokeAllScr.Load(ctx, client).Result()
	if err != nil {
		return nil, fmt.Errorf("load revoke_all.lua: %w", err)
	}

	_, err = fs.listScript.Load(ctx, client).Result()
	if err != nil {
		return nil, fmt.Errorf("load list_with_cleanup.lua: %w", err)
	}

	fs.scriptsReady = true
	return fs, nil
}

// Save 實作 FamilyStore.Save。
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

	// KEYS[1] = auth:family:{userID}:fid
	// KEYS[2] = auth:user_families:{userID}
	// ARGV[1] = family_state_json
	// ARGV[2] = abs_ttl_seconds
	// ARGV[3] = fid
	keys := []string{
		fmt.Sprintf("auth:family:{%s}:%s", state.UserID, state.FamilyID),
		fmt.Sprintf("auth:user_families:{%s}", state.UserID),
	}
	argv := []interface{}{
		string(stateJSON),
		strconv.FormatInt(absTTL, 10),
		state.FamilyID,
	}

	_, err = fs.saveScript.Run(ctx, fs.client, keys, argv...).Result()
	return err
}

// Rotate 實作 FamilyStore.Rotate。
func (fs *familyStore) Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string,
	graceWindow time.Duration) (RotateResult, *FamilyState, error) {
	if !fs.scriptsReady {
		return 0, nil, fmt.Errorf("family store not ready")
	}

	now := time.Now().Unix()
	graceWindowSeconds := int64(graceWindow.Seconds())

	// KEYS[1] = auth:family:{userID}:fid
	// KEYS[2] = auth:user_families:{userID}
	// ARGV[1] = presented_jti
	// ARGV[2] = new_jti
	// ARGV[3] = now_unix
	// ARGV[4] = grace_window_seconds
	// ARGV[5] = fid
	keys := []string{
		fmt.Sprintf("auth:family:{%s}:%s", userID, fid),
		fmt.Sprintf("auth:user_families:{%s}", userID),
	}
	argv := []interface{}{
		presentedJTI,
		newJTI,
		strconv.FormatInt(now, 10),
		strconv.FormatInt(graceWindowSeconds, 10),
		fid,
	}

	result, err := fs.rotateScript.Run(ctx, fs.client, keys, argv...).Result()
	if err != nil {
		return 0, nil, err
	}

	// Lua 回传 {code, state_json}
	arr, ok := result.([]interface{})
	if !ok || len(arr) < 2 {
		return 0, nil, fmt.Errorf("unexpected rotate script result type: %T", result)
	}

	code, ok := arr[0].(int64)
	if !ok {
		return 0, nil, fmt.Errorf("unexpected rotate code type: %T", arr[0])
	}

	stateJSON, ok := arr[1].(string)
	if !ok {
		return 0, nil, fmt.Errorf("unexpected rotate state_json type: %T", arr[1])
	}

	rotateResult := RotateResult(code)

	// 处理 ReplayDetected 和 FamilyNotFound，都回传 nil state
	if rotateResult == ReplayDetected || rotateResult == FamilyNotFound {
		return rotateResult, nil, nil
	}

	// 处理 Rotated 和 GraceHit：必须有 state_json，否则 fail-closed 视为 ErrFamilyNotFound
	if stateJSON == "" {
		return FamilyNotFound, nil, nil
	}

	var state FamilyState
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		// Lua 返回了錯誤的 JSON，fail-closed
		return FamilyNotFound, nil, nil
	}

	return rotateResult, &state, nil
}

// Revoke 實作 FamilyStore.Revoke。
func (fs *familyStore) Revoke(ctx context.Context, userID, fid string) error {
	if !fs.scriptsReady {
		return fmt.Errorf("family store not ready")
	}

	// KEYS[1] = auth:family:{userID}:fid
	// KEYS[2] = auth:user_families:{userID}
	// ARGV[1] = fid
	keys := []string{
		fmt.Sprintf("auth:family:{%s}:%s", userID, fid),
		fmt.Sprintf("auth:user_families:{%s}", userID),
	}
	argv := []interface{}{fid}

	_, err := fs.revokeScript.Run(ctx, fs.client, keys, argv...).Result()
	return err
}

// RevokeAll 實作 FamilyStore.RevokeAll。
func (fs *familyStore) RevokeAll(ctx context.Context, userID string) error {
	if !fs.scriptsReady {
		return fmt.Errorf("family store not ready")
	}

	// KEYS[1] = auth:user_families:{userID}
	// ARGV[1] = user_id
	keys := []string{
		fmt.Sprintf("auth:user_families:{%s}", userID),
	}
	argv := []interface{}{userID}

	_, err := fs.revokeAllScr.Run(ctx, fs.client, keys, argv...).Result()
	return err
}

// ListByUser 實作 FamilyStore.ListByUser。
func (fs *familyStore) ListByUser(ctx context.Context, userID string) ([]FamilyState, error) {
	if !fs.scriptsReady {
		return nil, fmt.Errorf("family store not ready")
	}

	// KEYS[1] = auth:user_families:{userID}
	// ARGV[1] = user_id
	keys := []string{
		fmt.Sprintf("auth:user_families:{%s}", userID),
	}
	argv := []interface{}{userID}

	result, err := fs.listScript.Run(ctx, fs.client, keys, argv...).Result()
	if err != nil {
		return nil, err
	}

	// Lua 回传 array of state_json strings
	arr, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected list script result type: %T", result)
	}

	var states []FamilyState
	for _, item := range arr {
		stateJSON, ok := item.(string)
		if !ok {
			// 跳过格式錯誤的项
			continue
		}

		var state FamilyState
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			// 跳过无法解析的项
			continue
		}
		states = append(states, state)
	}

	// 按 LastRotatedAt desc 排序（最近活躃的装置排在前面）
	sort.Slice(states, func(i, j int) bool {
		return states[i].LastRotatedAt > states[j].LastRotatedAt
	})

	return states, nil
}

// ScriptsLoaded 實作 FamilyStore.ScriptsLoaded。
func (fs *familyStore) ScriptsLoaded() bool {
	return fs.scriptsReady
}
