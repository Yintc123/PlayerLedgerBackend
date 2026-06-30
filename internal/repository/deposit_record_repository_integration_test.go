//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
)

// seedMember 建立一個 member 以滿足 deposit_records.player_id FK（ON DELETE RESTRICT）。
func seedMember(t *testing.T, db *gorm.DB, username string) uuid.UUID {
	t.Helper()
	m := &model.Member{
		Base:         model.Base{ID: uuid.New()},
		Username:     username,
		PasswordHash: "$2a$12$hash",
	}
	require.NoError(t, db.Create(m).Error)
	return m.ID
}

// newDepositRecord 組裝一筆最小可寫入的 deposit record。
func newDepositRecord(playerID uuid.UUID, status model.DepositStatus) *model.DepositRecord {
	return &model.DepositRecord{
		PlayerID:      playerID,
		PlayerName:    "player",
		Amount:        1000,
		Currency:      "TWD",
		Status:        status,
		PaymentMethod: model.PaymentMethodManual,
	}
}

// TestDepositRecordRepository_Create_FindByID_往返一致
func TestDepositRecordRepository_Create_FindByID_往返一致(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-create")
	rec := newDepositRecord(playerID, model.DepositStatusPending)

	require.NoError(t, repo.Create(ctx, rec))
	require.NotEqual(t, uuid.UUID{}, rec.ID, "Create 應由 DB 回填 id")

	found, err := repo.FindByID(ctx, rec.ID)
	require.NoError(t, err)
	assert.Equal(t, playerID, found.PlayerID)
	assert.Equal(t, int64(1000), found.Amount)
	assert.Equal(t, model.DepositStatusPending, found.Status)
	assert.False(t, found.CreatedAt.IsZero(), "autoCreateTime 應填入 created_at")
}

// TestDepositRecordRepository_FindByID_不存在_回傳ErrNotFound
func TestDepositRecordRepository_FindByID_不存在_回傳ErrNotFound(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestDepositRecordRepository_Create_ReferenceNo重複_回傳ErrReferenceNoConflict
func TestDepositRecordRepository_Create_ReferenceNo重複_回傳ErrReferenceNoConflict(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-dup-ref")
	ref := "TXN-DUP-001"

	rec1 := newDepositRecord(playerID, model.DepositStatusPending)
	rec1.ReferenceNo = &ref
	require.NoError(t, repo.Create(ctx, rec1))

	rec2 := newDepositRecord(playerID, model.DepositStatusPending)
	rec2.ReferenceNo = &ref
	err := repo.Create(ctx, rec2)
	assert.ErrorIs(t, err, apperr.ErrReferenceNoConflict)
}

// TestDepositRecordRepository_List_Status篩選
func TestDepositRecordRepository_List_Status篩選(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-list-status")
	require.NoError(t, repo.Create(ctx, newDepositRecord(playerID, model.DepositStatusPending)))
	require.NoError(t, repo.Create(ctx, newDepositRecord(playerID, model.DepositStatusCompleted)))
	require.NoError(t, repo.Create(ctx, newDepositRecord(playerID, model.DepositStatusCompleted)))

	records, total, err := repo.List(ctx, DepositRecordFilter{
		Status:   []model.DepositStatus{model.DepositStatusCompleted},
		Page:     1,
		PageSize: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	for _, r := range records {
		assert.Equal(t, model.DepositStatusCompleted, r.Status)
	}
}

// TestDepositRecordRepository_List_PaymentMethod篩選
func TestDepositRecordRepository_List_PaymentMethod篩選(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-list-pm")
	manual := newDepositRecord(playerID, model.DepositStatusPending)
	bank := newDepositRecord(playerID, model.DepositStatusPending)
	bank.PaymentMethod = model.PaymentMethodBankTransfer
	require.NoError(t, repo.Create(ctx, manual))
	require.NoError(t, repo.Create(ctx, bank))

	records, total, err := repo.List(ctx, DepositRecordFilter{
		PaymentMethod: []model.PaymentMethod{model.PaymentMethodBankTransfer},
		Page:          1,
		PageSize:      20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, records, 1)
	assert.Equal(t, model.PaymentMethodBankTransfer, records[0].PaymentMethod)
}

// TestDepositRecordRepository_List_AmountSort
func TestDepositRecordRepository_List_AmountSort(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-sort")
	small := newDepositRecord(playerID, model.DepositStatusPending)
	small.Amount = 100
	large := newDepositRecord(playerID, model.DepositStatusPending)
	large.Amount = 9000
	require.NoError(t, repo.Create(ctx, small))
	require.NoError(t, repo.Create(ctx, large))

	records, _, err := repo.List(ctx, DepositRecordFilter{
		Sort:     "-amount",
		Page:     1,
		PageSize: 20,
	})
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, int64(9000), records[0].Amount, "-amount 應由大到小")
	assert.Equal(t, int64(100), records[1].Amount)
}

// TestDepositRecordRepository_Update_狀態與備註
func TestDepositRecordRepository_Update_狀態與備註(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-update")
	rec := newDepositRecord(playerID, model.DepositStatusPending)
	require.NoError(t, repo.Create(ctx, rec))

	newStatus := model.DepositStatusCompleted
	note := "已確認入帳"
	notePtr := &note
	updated, err := repo.Update(ctx, rec.ID, UpdateDepositInput{
		NewStatus:    &newStatus,
		InternalNote: &notePtr,
	})
	require.NoError(t, err)
	assert.Equal(t, model.DepositStatusCompleted, updated.Status)
	require.NotNil(t, updated.InternalNote)
	assert.Equal(t, "已確認入帳", *updated.InternalNote)
}

// TestDepositRecordRepository_Update_三態清空備註
func TestDepositRecordRepository_Update_三態清空備註(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerID := seedMember(t, db, "player-clear-note")
	rec := newDepositRecord(playerID, model.DepositStatusPending)
	existing := "舊備註"
	rec.DisplayNote = &existing
	require.NoError(t, repo.Create(ctx, rec))

	// 傳 &nil（outer non-nil, inner nil）→ 清空
	var nilStr *string
	updated, err := repo.Update(ctx, rec.ID, UpdateDepositInput{
		DisplayNote: &nilStr,
	})
	require.NoError(t, err)
	assert.Nil(t, updated.DisplayNote, "&nil 應清空 display_note")
}

// TestDepositRecordRepository_Update_不存在_回傳ErrNotFound
func TestDepositRecordRepository_Update_不存在_回傳ErrNotFound(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	newStatus := model.DepositStatusCompleted
	_, err := repo.Update(ctx, uuid.New(), UpdateDepositInput{NewStatus: &newStatus})
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestDepositRecordRepository_ListByPlayer_資料隔離
func TestDepositRecordRepository_ListByPlayer_資料隔離(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()

	playerA := seedMember(t, db, "player-A")
	playerB := seedMember(t, db, "player-B")
	require.NoError(t, repo.Create(ctx, newDepositRecord(playerA, model.DepositStatusPending)))
	require.NoError(t, repo.Create(ctx, newDepositRecord(playerA, model.DepositStatusCompleted)))
	require.NoError(t, repo.Create(ctx, newDepositRecord(playerB, model.DepositStatusPending)))

	records, total, err := repo.ListByPlayer(ctx, playerA, PlayerDepositFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	for _, r := range records {
		assert.Equal(t, playerA, r.PlayerID, "只應回傳 playerA 自己的紀錄")
	}
}

// ─── AggregateByPlayer（players-deposit-summary-api §3/§4）────────────────────

func TestDepositRecordRepository_AggregateByPlayer_分桶與生涯(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()
	pid := seedMember(t, db, "agg-player")

	mk := func(status model.DepositStatus, amount int64, created time.Time) {
		rec := newDepositRecord(pid, status)
		rec.Amount = amount
		rec.CreatedAt = created
		require.NoError(t, repo.Create(ctx, rec))
	}
	jan4 := time.Date(2026, 1, 4, 9, 0, 0, 0, time.UTC)
	jun20 := time.Date(2026, 6, 20, 3, 0, 0, 0, time.UTC)
	mk(model.DepositStatusCompleted, 10000, jan4)
	mk(model.DepositStatusCompleted, 14800, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	mk(model.DepositStatusRefunded, 1200, jun20)
	mk(model.DepositStatusFailed, 500, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	mk(model.DepositStatusPending, 999, time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC))   // 排除
	mk(model.DepositStatusCancelled, 888, time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)) // 排除

	agg, err := repo.AggregateByPlayer(ctx, pid)
	require.NoError(t, err)
	require.Len(t, agg.Totals, 1)
	tw := agg.Totals[0]
	assert.Equal(t, "TWD", tw.Currency)
	assert.Equal(t, int64(2), tw.CompletedCount)
	assert.Equal(t, int64(24800), tw.CompletedAmount)
	assert.Equal(t, int64(1), tw.RefundedCount)
	assert.Equal(t, int64(1200), tw.RefundedAmount)
	assert.Equal(t, int64(1), tw.FailedCount)
	require.NotNil(t, agg.FirstTopupAt)
	require.NotNil(t, agg.LastTopupAt)
	assert.Equal(t, jan4.Unix(), agg.FirstTopupAt.Unix(), "首次取成功口徑 created_at 最小")
	assert.Equal(t, jun20.Unix(), agg.LastTopupAt.Unix(), "末次取成功口徑 created_at 最大")
	require.NotNil(t, agg.LifetimeDays)
	assert.Equal(t, 167, *agg.LifetimeDays, "UTC 日曆日差 Jan4→Jun20 = 167")
}

func TestDepositRecordRepository_AggregateByPlayer_無紀錄_空彙總(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()
	pid := seedMember(t, db, "agg-empty")

	agg, err := repo.AggregateByPlayer(ctx, pid)
	require.NoError(t, err)
	assert.Empty(t, agg.Totals)
	assert.Nil(t, agg.FirstTopupAt)
	assert.Nil(t, agg.LastTopupAt)
	assert.Nil(t, agg.LifetimeDays)
}

func TestDepositRecordRepository_AggregateByPlayer_僅失敗_無首末次(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()
	pid := seedMember(t, db, "agg-failed")

	rec := newDepositRecord(pid, model.DepositStatusFailed)
	rec.Amount = 700
	require.NoError(t, repo.Create(ctx, rec))

	agg, err := repo.AggregateByPlayer(ctx, pid)
	require.NoError(t, err)
	require.Len(t, agg.Totals, 1)
	assert.Equal(t, int64(1), agg.Totals[0].FailedCount)
	assert.Equal(t, int64(0), agg.Totals[0].CompletedAmount)
	assert.Nil(t, agg.FirstTopupAt, "僅失敗無成功口徑紀錄 → 首次 nil")
	assert.Nil(t, agg.LifetimeDays)
}

func TestDepositRecordRepository_AggregateByPlayer_同日多筆_生涯0(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()
	pid := seedMember(t, db, "agg-sameday")

	day := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	r1 := newDepositRecord(pid, model.DepositStatusCompleted)
	r1.CreatedAt = day.Add(2 * time.Hour)
	r2 := newDepositRecord(pid, model.DepositStatusCompleted)
	r2.CreatedAt = day.Add(20 * time.Hour)
	require.NoError(t, repo.Create(ctx, r1))
	require.NoError(t, repo.Create(ctx, r2))

	agg, err := repo.AggregateByPlayer(ctx, pid)
	require.NoError(t, err)
	require.NotNil(t, agg.LifetimeDays)
	assert.Equal(t, 0, *agg.LifetimeDays, "同一 UTC 日期 → 生涯 0 天")
}

func TestDepositRecordRepository_AggregateByPlayer_僅查指定玩家(t *testing.T) {
	db := WithTx(t)
	repo := NewDepositRecordRepository(db)
	ctx := context.Background()
	pidA := seedMember(t, db, "agg-iso-a")
	pidB := seedMember(t, db, "agg-iso-b")

	a := newDepositRecord(pidA, model.DepositStatusCompleted)
	a.Amount = 100
	require.NoError(t, repo.Create(ctx, a))
	b := newDepositRecord(pidB, model.DepositStatusCompleted)
	b.Amount = 999
	require.NoError(t, repo.Create(ctx, b))

	agg, err := repo.AggregateByPlayer(ctx, pidA)
	require.NoError(t, err)
	require.Len(t, agg.Totals, 1)
	assert.Equal(t, int64(100), agg.Totals[0].CompletedAmount, "不應混入其他玩家紀錄")
}
