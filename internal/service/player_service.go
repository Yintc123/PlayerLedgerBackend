package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
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

// CurrencyTotals 單一幣別彙總（含 service 以金額計算的 refund_rate）。
type CurrencyTotals struct {
	Currency        string
	CompletedCount  int64
	CompletedAmount int64
	RefundedCount   int64
	RefundedAmount  int64
	FailedCount     int64
	RefundRate      float64 // refunded/(completed+refunded)，0..1，四捨五入 4 位
}

// DepositSummaryOutput 玩家儲值彙總 service 輸出（players-deposit-summary-api.md §3）。
type DepositSummaryOutput struct {
	PlayerID     uuid.UUID
	Totals       []CurrencyTotals // 依 currency ASC；無 → 空 slice
	FirstTopupAt *time.Time
	LastTopupAt  *time.Time
	LifetimeDays *int
}

// depositAggregator 是 PlayerService 對儲值聚合的最小依賴（interface segregation）。
// 由 repository.DepositRecordRepository 滿足。
type depositAggregator interface {
	AggregateByPlayer(ctx context.Context, playerID uuid.UUID) (repository.DepositAggregate, error)
}

// PlayerService 玩家查詢業務介面（players-api.md §6 / players-deposit-summary-api.md）。
type PlayerService interface {
	// Search 解碼 cursor → keyset，取 pageSize+1 判斷 hasMore，砍尾並編 NextCursor，寫 players.search audit。
	// cursor 解碼失敗回 apperr.ErrInvalidInput。
	Search(ctx context.Context, in PlayerSearchInput) (PlayerSearchOutput, error)
	// Get 取玩家；不存在 / 已軟刪除回 apperr.ErrNotFound。寫 players.read audit。
	Get(ctx context.Context, id uuid.UUID) (*model.Member, error)
	// DepositSummary 取玩家儲值彙總；玩家不存在 / 已軟刪除回 apperr.ErrNotFound。
	// 成功時寫 players.deposit_summary audit（僅識別碼，不記金額）。
	DepositSummary(ctx context.Context, id uuid.UUID) (*DepositSummaryOutput, error)
}

type playerService struct {
	memberRepo  repository.MemberRepository
	depositRepo depositAggregator
	audit       audit.Logger
}

// NewPlayerService 建立玩家查詢服務。
func NewPlayerService(memberRepo repository.MemberRepository, depositRepo depositAggregator, auditLogger audit.Logger) PlayerService {
	return &playerService{memberRepo: memberRepo, depositRepo: depositRepo, audit: auditLogger}
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

func (s *playerService) DepositSummary(ctx context.Context, id uuid.UUID) (*DepositSummaryOutput, error) {
	// 玩家存在性（soft-delete-aware，同 GET /cms/players/{id}）；不存在 / 軟刪 → ErrNotFound。
	if _, err := s.memberRepo.FindByID(ctx, id); err != nil {
		return nil, err
	}

	agg, err := s.depositRepo.AggregateByPlayer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("aggregate deposits: %w", err)
	}

	totals := make([]CurrencyTotals, 0, len(agg.Totals))
	for _, a := range agg.Totals {
		totals = append(totals, CurrencyTotals{
			Currency:        a.Currency,
			CompletedCount:  a.CompletedCount,
			CompletedAmount: a.CompletedAmount,
			RefundedCount:   a.RefundedCount,
			RefundedAmount:  a.RefundedAmount,
			FailedCount:     a.FailedCount,
			RefundRate:      refundRate(a.CompletedAmount, a.RefundedAmount),
		})
	}

	actor := ctxkey.ActorFrom(ctx)
	s.audit.Log(ctx, audit.AuthEvent{
		Type:   audit.EventPlayerDepositSummary,
		UserID: actor.UserID,
		Extra: map[string]any{
			"role":             actor.Role,
			"target_player_id": id.String(),
		},
	})

	return &DepositSummaryOutput{
		PlayerID:     id,
		Totals:       totals,
		FirstTopupAt: agg.FirstTopupAt,
		LastTopupAt:  agg.LastTopupAt,
		LifetimeDays: agg.LifetimeDays,
	}, nil
}

// refundRate = refunded_amount / (completed_amount + refunded_amount)（金額比，§3.3）。
// 分母為 0 → 0；四捨五入至小數 4 位（round half away from zero，math.Round 語意）。
func refundRate(completedAmount, refundedAmount int64) float64 {
	denom := completedAmount + refundedAmount
	if denom == 0 {
		return 0
	}
	r := float64(refundedAmount) / float64(denom)
	return math.Round(r*10000) / 10000
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
