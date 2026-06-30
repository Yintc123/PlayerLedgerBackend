package service

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
)

// ─── fake member repo（keyset-aware）─────────────────────────────────────────

type fakePlayerRepo struct {
	members []*model.Member
}

func (r *fakePlayerRepo) FindByUsername(_ context.Context, _ string) (*model.Member, error) {
	return nil, apperr.ErrNotFound
}

func (r *fakePlayerRepo) FindByID(_ context.Context, id uuid.UUID) (*model.Member, error) {
	for _, m := range r.members {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, apperr.ErrNotFound
}

func (r *fakePlayerRepo) Search(_ context.Context, f repository.PlayerSearchFilter) ([]*model.Member, error) {
	var out []*model.Member
	for _, m := range r.members {
		if f.DisplayNamePrefix != nil &&
			!strings.HasPrefix(strings.ToLower(m.DisplayName), strings.ToLower(*f.DisplayNamePrefix)) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	if f.After != nil {
		var filtered []*model.Member
		for _, m := range out {
			if m.CreatedAt.Before(f.After.CreatedAt) ||
				(m.CreatedAt.Equal(f.After.CreatedAt) && m.ID.String() < f.After.ID.String()) {
				filtered = append(filtered, m)
			}
		}
		out = filtered
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func seedMembers(n int) *fakePlayerRepo {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakePlayerRepo{}
	for i := 0; i < n; i++ {
		repo.members = append(repo.members, &model.Member{
			Base:        model.Base{ID: uuid.New(), CreatedAt: base.Add(time.Duration(i) * time.Hour)},
			Username:    "u",
			DisplayName: "Player",
			Status:      model.MemberStatusActive,
		})
	}
	return repo
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestPlayerService_Search_前綴條件_回傳符合玩家(t *testing.T) {
	repo := &fakePlayerRepo{members: []*model.Member{
		{Base: model.Base{ID: uuid.New(), CreatedAt: time.Now()}, DisplayName: "Alice"},
		{Base: model.Base{ID: uuid.New(), CreatedAt: time.Now()}, DisplayName: "Bob"},
	}}
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), audit.NewNopLogger())

	out, err := svc.Search(context.Background(), PlayerSearchInput{
		DisplayName: strptr("Ali"),
		Limit:       20,
	})
	require.NoError(t, err)
	require.Len(t, out.Players, 1)
	assert.Equal(t, "Alice", out.Players[0].DisplayName)
	assert.Nil(t, out.NextCursor, "單頁結果 next_cursor 應為 nil")
}

func TestPlayerService_Search_Keyset翻頁_不重不漏(t *testing.T) {
	repo := seedMembers(5)
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), audit.NewNopLogger())
	ctx := context.Background()

	seen := map[uuid.UUID]bool{}
	var cursor *string
	pages := 0
	for {
		out, err := svc.Search(ctx, PlayerSearchInput{DisplayName: strptr("Player"), Cursor: cursor, Limit: 2})
		require.NoError(t, err)
		for _, m := range out.Players {
			assert.False(t, seen[m.ID], "玩家 %s 不應跨頁重複", m.ID)
			seen[m.ID] = true
		}
		pages++
		require.LessOrEqual(t, pages, 10, "分頁不應無限迴圈")
		if out.NextCursor == nil {
			break
		}
		cursor = out.NextCursor
	}
	assert.Len(t, seen, 5, "應走訪全部 5 名玩家，不重不漏")
}

func TestPlayerService_Search_最後一頁_NextCursor為nil(t *testing.T) {
	repo := seedMembers(2)
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), audit.NewNopLogger())

	out, err := svc.Search(context.Background(), PlayerSearchInput{DisplayName: strptr("Player"), Limit: 20})
	require.NoError(t, err)
	assert.Len(t, out.Players, 2)
	assert.Nil(t, out.NextCursor)
}

func TestPlayerService_Search_有下一頁_NextCursor非nil(t *testing.T) {
	repo := seedMembers(3)
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), audit.NewNopLogger())

	out, err := svc.Search(context.Background(), PlayerSearchInput{DisplayName: strptr("Player"), Limit: 2})
	require.NoError(t, err)
	assert.Len(t, out.Players, 2, "limit=2 應只回 2 筆")
	require.NotNil(t, out.NextCursor, "尚有第 3 筆，next_cursor 不應為 nil")
}

func TestPlayerService_Search_非法cursor_回ErrInvalidInput(t *testing.T) {
	repo := seedMembers(1)
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), audit.NewNopLogger())

	bad := "!!!not-base64!!!"
	_, err := svc.Search(context.Background(), PlayerSearchInput{DisplayName: strptr("Player"), Cursor: &bad, Limit: 20})
	assert.ErrorIs(t, err, apperr.ErrInvalidInput)
}

func TestPlayerService_Search_寫players_search_audit(t *testing.T) {
	repo := seedMembers(2)
	cap := &captureAudit{}
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), cap)

	_, err := svc.Search(context.Background(), PlayerSearchInput{DisplayName: strptr("Player"), Limit: 20})
	require.NoError(t, err)
	require.Len(t, cap.events, 1)
	assert.Equal(t, audit.EventPlayerSearch, cap.events[0].Type)
	assert.Equal(t, 2, cap.events[0].Extra["result_count"])
}

func TestPlayerService_Get_存在_回玩家並寫audit(t *testing.T) {
	id := uuid.New()
	repo := &fakePlayerRepo{members: []*model.Member{
		{Base: model.Base{ID: id, CreatedAt: time.Now()}, DisplayName: "Alice"},
	}}
	cap := &captureAudit{}
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), cap)

	m, err := svc.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "Alice", m.DisplayName)
	require.Len(t, cap.events, 1)
	assert.Equal(t, audit.EventPlayerRead, cap.events[0].Type)
	assert.Equal(t, id.String(), cap.events[0].Extra["target_player_id"])
}

func TestPlayerService_Get_不存在_回ErrNotFound(t *testing.T) {
	repo := &fakePlayerRepo{}
	svc := NewPlayerService(repo, repository.NewFakeDepositRecordRepository(), audit.NewNopLogger())

	_, err := svc.Get(context.Background(), uuid.New())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// ─── DepositSummary（players-deposit-summary-api §3/§7）──────────────────────

type stubAggregator struct {
	agg    repository.DepositAggregate
	err    error
	called bool
}

func (s *stubAggregator) AggregateByPlayer(_ context.Context, _ uuid.UUID) (repository.DepositAggregate, error) {
	s.called = true
	return s.agg, s.err
}

func TestPlayerService_DepositSummary_玩家不存在_回ErrNotFound不寫audit(t *testing.T) {
	repo := &fakePlayerRepo{} // 無 member
	agg := &stubAggregator{}
	cap := &captureAudit{}
	svc := NewPlayerService(repo, agg, cap)

	_, err := svc.DepositSummary(context.Background(), uuid.New())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.False(t, agg.called, "玩家不存在時不應呼叫聚合")
	assert.Empty(t, cap.events, "玩家不存在時不應寫 audit")
}

func TestPlayerService_DepositSummary_退款率金額比四捨五入4位(t *testing.T) {
	id := uuid.New()
	repo := &fakePlayerRepo{members: []*model.Member{{Base: model.Base{ID: id, CreatedAt: time.Now()}}}}
	agg := &stubAggregator{agg: repository.DepositAggregate{Totals: []repository.CurrencyAggregate{{
		Currency: "TWD", CompletedCount: 12, CompletedAmount: 24800,
		RefundedCount: 1, RefundedAmount: 1200, FailedCount: 2,
	}}}}
	svc := NewPlayerService(repo, agg, &captureAudit{})

	out, err := svc.DepositSummary(context.Background(), id)
	require.NoError(t, err)
	require.Len(t, out.Totals, 1)
	// 1200 / (24800+1200) = 0.046153… → 0.0462
	assert.InDelta(t, 0.0462, out.Totals[0].RefundRate, 1e-9)
}

func TestPlayerService_DepositSummary_分母為零_退款率為0(t *testing.T) {
	id := uuid.New()
	repo := &fakePlayerRepo{members: []*model.Member{{Base: model.Base{ID: id}}}}
	agg := &stubAggregator{agg: repository.DepositAggregate{Totals: []repository.CurrencyAggregate{{
		Currency: "TWD", FailedCount: 3, // 無 completed / refunded
	}}}}
	svc := NewPlayerService(repo, agg, &captureAudit{})

	out, err := svc.DepositSummary(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 0.0, out.Totals[0].RefundRate)
}

func TestPlayerService_DepositSummary_玩家存在無紀錄_空彙總(t *testing.T) {
	id := uuid.New()
	repo := &fakePlayerRepo{members: []*model.Member{{Base: model.Base{ID: id}}}}
	agg := &stubAggregator{agg: repository.DepositAggregate{Totals: []repository.CurrencyAggregate{}}}
	svc := NewPlayerService(repo, agg, &captureAudit{})

	out, err := svc.DepositSummary(context.Background(), id)
	require.NoError(t, err)
	assert.Empty(t, out.Totals)
	assert.Nil(t, out.FirstTopupAt)
	assert.Nil(t, out.LastTopupAt)
	assert.Nil(t, out.LifetimeDays)
}

func TestPlayerService_DepositSummary_成功_寫audit並透傳時間(t *testing.T) {
	id := uuid.New()
	first := time.Date(2026, 1, 4, 9, 0, 0, 0, time.UTC)
	last := time.Date(2026, 6, 20, 3, 0, 0, 0, time.UTC)
	days := 167
	repo := &fakePlayerRepo{members: []*model.Member{{Base: model.Base{ID: id}}}}
	agg := &stubAggregator{agg: repository.DepositAggregate{
		Totals:       []repository.CurrencyAggregate{{Currency: "TWD", CompletedCount: 1, CompletedAmount: 1000}},
		FirstTopupAt: &first, LastTopupAt: &last, LifetimeDays: &days,
	}}
	cap := &captureAudit{}
	svc := NewPlayerService(repo, agg, cap)

	out, err := svc.DepositSummary(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, out.FirstTopupAt)
	assert.Equal(t, first, *out.FirstTopupAt)
	assert.Equal(t, last, *out.LastTopupAt)
	require.NotNil(t, out.LifetimeDays)
	assert.Equal(t, 167, *out.LifetimeDays)
	require.Len(t, cap.events, 1)
	assert.Equal(t, audit.EventPlayerDepositSummary, cap.events[0].Type)
	assert.Equal(t, id.String(), cap.events[0].Extra["target_player_id"])
}
