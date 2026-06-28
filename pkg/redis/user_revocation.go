package redis

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// UserRevocationStore 提供「對單一 user 一次性廢掉所有當下還活著的 access token」的 watermark
// 機制（§7.5）。與 §7.3 AccessTokenBlacklist 互補：blacklist 是「caller 知道目標 jti」的精準
// 撤銷，而 UserRevocationStore 是「caller 不知道任何 jti」（admin 改別人 role / 軟刪等）的整
// user 廢票。AuthMiddleware verify 成功後比對 claims.iat < watermark 即視為 invalid。
type UserRevocationStore interface {
	// Revoke 將 userID 的 revocation watermark 設為 now() unix seconds。
	//   - ttl：key 存活時間，建議「系統最長 abs_exp」+ 安全餘量（例如 ios refresh ttl = 30d）。
	//     ttl ≤ 0 視為 no-op（呼叫端應自行處理上游邊界）。
	//   - Redis 寫入失敗：回 error。caller（cms_user_service.Update / SoftDelete）應 log + metric，
	//     但不影響「廢 family」此一更關鍵步驟。
	Revoke(ctx context.Context, userID string, ttl time.Duration) error

	// RevokedAfter 查詢 userID 的 revocation watermark unix seconds。
	//   - 找不到 key                                → (0, nil)：從未被 revoke
	//   - 命中                                       → (unix_ts, nil)
	//   - Redis 故障（連線錯 / timeout / 解析錯）→ (0, err)：caller fail-open
	//     AuthMiddleware 收到 err 後 log warn + AuthUserRevokeErrors.Inc() + 放行。
	RevokedAfter(ctx context.Context, userID string) (int64, error)
}

type userRevocationStore struct {
	client *redis.Client
	now    func() time.Time
}

// NewUserRevocationStore 由 *redis.Client 構造預設實作。
// 使用原生 SET EX + GET 兩條命令，無 Lua（單 key 操作）。
func NewUserRevocationStore(client *redis.Client) UserRevocationStore {
	return &userRevocationStore{client: client, now: time.Now}
}

func userRevocationKey(userID string) string {
	// hash tag {<userID>} 與 §7.4 family store 一致，未來 Cluster 環境下同一 user 的所有 auth
	// key 落在同一 slot，避免 cross-slot 限制。
	return "auth:user_revoked_after:{" + userID + "}"
}

// Revoke 實作 UserRevocationStore.Revoke。
// 使用 SET key value EX ttl：value = now() 的 unix seconds 字串。
func (s *userRevocationStore) Revoke(ctx context.Context, userID string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	key := userRevocationKey(userID)
	val := strconv.FormatInt(s.now().Unix(), 10)
	return s.client.Set(ctx, key, val, ttl).Err()
}

// RevokedAfter 實作 UserRevocationStore.RevokedAfter。
// 使用 GET：key 不存在回 (0, nil)；存在解析為 int64；Redis 故障或解析失敗回 (0, err)。
func (s *userRevocationStore) RevokedAfter(ctx context.Context, userID string) (int64, error) {
	key := userRevocationKey(userID)
	val, err := s.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	ts, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, err
	}
	return ts, nil
}
