package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/ctxkey"
)

// PlayerSearchInput handler 解析 query 後傳入；字串已完成 trim / lowercase / 正規化。
// 至少一個搜尋條件非 nil（handler 保證）；Limit 為 pageSize（已套預設 20 與上限 50 驗證）。
type PlayerSearchInput struct {
	PlayerID    *uuid.UUID
	ExternalID  *string
	DisplayName *string // 前綴
	Email       *string // 前綴（lowercase）
	Phone       *string // 正規化
	Cursor      *string // opaque keyset cursor
	Limit       int     // = pageSize（1..50）
}

// PlayerSearchOutput Search 結果；Players 已砍至 pageSize 筆。
type PlayerSearchOutput struct {
	Players    []*model.Member // 已砍至 pageSize 筆
	NextCursor *string         // 最後一頁為 nil
}

// PlayerService 玩家查詢業務介面（players-api.md §6）。
type PlayerService interface {
	// Search 解碼 cursor → keyset，取 pageSize+1 判斷 hasMore，砍尾並編 NextCursor，寫 players.search audit。
	// cursor 解碼失敗回 apperr.ErrInvalidInput。
	Search(ctx context.Context, in PlayerSearchInput) (PlayerSearchOutput, error)
	// Get 取玩家；不存在 / 已軟刪除回 apperr.ErrNotFound。寫 players.read audit。
	Get(ctx context.Context, id uuid.UUID) (*model.Member, error)
}

type playerService struct {
	memberRepo repository.MemberRepository
	audit      audit.Logger
}

// NewPlayerService 建立玩家查詢服務。
func NewPlayerService(memberRepo repository.MemberRepository, auditLogger audit.Logger) PlayerService {
	return &playerService{memberRepo: memberRepo, audit: auditLogger}
}

func (s *playerService) Search(ctx context.Context, in PlayerSearchInput) (PlayerSearchOutput, error) {
	pageSize := in.Limit
	if pageSize < 1 {
		pageSize = 20
	}

	filter := repository.PlayerSearchFilter{
		PlayerID:          in.PlayerID,
		ExternalID:        in.ExternalID,
		DisplayNamePrefix: in.DisplayName,
		EmailPrefix:       in.Email,
		Phone:             in.Phone,
		Limit:             pageSize + 1, // 多取一筆判斷是否還有下一頁
	}

	if in.Cursor != nil {
		after, err := decodePlayerCursor(*in.Cursor)
		if err != nil {
			return PlayerSearchOutput{}, err
		}
		filter.After = after
	}

	members, err := s.memberRepo.Search(ctx, filter)
	if err != nil {
		return PlayerSearchOutput{}, fmt.Errorf("search players: %w", err)
	}

	var nextCursor *string
	if len(members) > pageSize {
		members = members[:pageSize]
		c := encodePlayerCursor(members[len(members)-1])
		nextCursor = &c
	}

	actor := ctxkey.ActorFrom(ctx)
	s.audit.Log(ctx, audit.AuthEvent{
		Type:   audit.EventPlayerSearch,
		UserID: actor.UserID,
		Extra: map[string]any{
			"role":         actor.Role,
			"result_count": len(members),
			"fields":       providedSearchFields(in), // 去敏：僅記欄位名，不記 PII 原值
		},
	})

	return PlayerSearchOutput{Players: members, NextCursor: nextCursor}, nil
}

func (s *playerService) Get(ctx context.Context, id uuid.UUID) (*model.Member, error) {
	member, err := s.memberRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	actor := ctxkey.ActorFrom(ctx)
	s.audit.Log(ctx, audit.AuthEvent{
		Type:   audit.EventPlayerRead,
		UserID: actor.UserID,
		Extra: map[string]any{
			"role":             actor.Role,
			"target_player_id": id.String(),
		},
	})

	return member, nil
}

// providedSearchFields 回報哪些搜尋欄位有提供（去敏，不含值），供 audit query 概要用。
func providedSearchFields(in PlayerSearchInput) []string {
	fields := make([]string, 0, 5)
	if in.PlayerID != nil {
		fields = append(fields, "player_id")
	}
	if in.ExternalID != nil {
		fields = append(fields, "external_id")
	}
	if in.DisplayName != nil {
		fields = append(fields, "display_name")
	}
	if in.Email != nil {
		fields = append(fields, "email")
	}
	if in.Phone != nil {
		fields = append(fields, "phone")
	}
	return fields
}

// ─── keyset cursor（opaque：base64url(JSON{t, id})）───────────────────────────

type playerCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"id"`
}

func encodePlayerCursor(m *model.Member) string {
	b, _ := json.Marshal(playerCursor{CreatedAt: m.CreatedAt, ID: m.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePlayerCursor(s string) (*repository.Keyset, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, apperr.ErrInvalidInput
	}
	var c playerCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, apperr.ErrInvalidInput
	}
	if c.ID == (uuid.UUID{}) || c.CreatedAt.IsZero() {
		return nil, apperr.ErrInvalidInput
	}
	return &repository.Keyset{CreatedAt: c.CreatedAt, ID: c.ID}, nil
}
