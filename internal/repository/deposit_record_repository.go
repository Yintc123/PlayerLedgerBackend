package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

// UpdateDepositInput 採三態語意處理可空備註欄位（純儲存關切）：
//
//	nil ptr  = 欄位未提供，不修改現有值
//	&nil     = 明確傳 null，清空該欄位
//	&"text"  = 設定新值
type UpdateDepositInput struct {
	NewStatus    *model.DepositStatus
	InternalNote **string
	DisplayNote  **string
}

// DepositRecordFilter CMS 列表篩選條件。
type DepositRecordFilter struct {
	PlayerID      *uuid.UUID
	Status        []model.DepositStatus
	PaymentMethod []model.PaymentMethod
	StartDate     *time.Time
	EndDate       *time.Time
	// Sort 白名單（handler 驗證後傳入，repository 直接映射至 ORDER BY）：
	//   "-created_at" → ORDER BY created_at DESC（預設）
	//   "created_at"  → ORDER BY created_at ASC
	//   "-amount"     → ORDER BY amount DESC
	//   "amount"      → ORDER BY amount ASC
	Sort     string
	Page     int
	PageSize int
}

// PlayerDepositFilter 玩家端查詢；player_id 由 service 從 token 取得後傳入。
type PlayerDepositFilter struct {
	StartDate *time.Time
	EndDate   *time.Time
	Page      int
	PageSize  int
}

// CurrencyAggregate 單一幣別的分桶統計（players-deposit-summary-api.md §3.1）。
// 計數 / 金額依當前 status 互斥分桶；refund_rate 不在此層（由 service 以金額計算）。
type CurrencyAggregate struct {
	Currency        string
	CompletedCount  int64
	CompletedAmount int64
	RefundedCount   int64
	RefundedAmount  int64
	FailedCount     int64
}

// DepositAggregate 玩家層級儲值彙總聚合結果（players-deposit-summary-api.md §3）。
type DepositAggregate struct {
	Totals       []CurrencyAggregate // 依 currency ASC；無可彙總紀錄 → 空 slice
	FirstTopupAt *time.Time          // completed ∪ refunded 的 created_at 最小；無 → nil
	LastTopupAt  *time.Time          // 同上最大；無 → nil
	LifetimeDays *int                // UTC 日曆日差 DATE(last)-DATE(first)；無成功紀錄 → nil
}

// DepositRecordRepository 儲值紀錄倉儲介面。
type DepositRecordRepository interface {
	Create(ctx context.Context, r *model.DepositRecord) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.DepositRecord, error)
	List(ctx context.Context, f DepositRecordFilter) ([]*model.DepositRecord, int64, error)
	// Update 回傳更新後的 record，避免 handler 需要再查一次 DB。
	Update(ctx context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error)
	ListByPlayer(ctx context.Context, playerID uuid.UUID, f PlayerDepositFilter) ([]*model.DepositRecord, int64, error)
	// AggregateByPlayer 聚合單一玩家的儲值統計（players-deposit-summary-api.md §3/§4）。
	// 於唯讀 REPEATABLE READ 交易內跑兩條查詢，確保各幣別統計與首末次時間取自同一快照。
	AggregateByPlayer(ctx context.Context, playerID uuid.UUID) (DepositAggregate, error)
}

// reportableStatuses 計入 totals_by_currency 的狀態；pending / cancelled 排除。
var reportableStatuses = []model.DepositStatus{
	model.DepositStatusCompleted, model.DepositStatusRefunded, model.DepositStatusFailed,
}

// successStatuses 計入首末次儲值 / 生涯天數的「成功口徑」狀態。
var successStatuses = []model.DepositStatus{
	model.DepositStatusCompleted, model.DepositStatusRefunded,
}

// depositSortMap 白名單映射，防止 SQL injection。
var depositSortMap = map[string]string{
	"-created_at": "created_at DESC",
	"created_at":  "created_at ASC",
	"-amount":     "amount DESC",
	"amount":      "amount ASC",
}

type depositRecordRepository struct {
	db *gorm.DB
}

func NewDepositRecordRepository(db *gorm.DB) DepositRecordRepository {
	return &depositRecordRepository{db: db}
}

func (r *depositRecordRepository) Create(ctx context.Context, rec *model.DepositRecord) error {
	if err := dbFromCtx(ctx, r.db).WithContext(ctx).Create(rec).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return apperr.ErrReferenceNoConflict
		}
		return fmt.Errorf("create deposit record: %w", err)
	}
	return nil
}

func (r *depositRecordRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.DepositRecord, error) {
	var rec model.DepositRecord
	if err := dbFromCtx(ctx, r.db).WithContext(ctx).Where("id = ?", id).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("find deposit record: %w", err)
	}
	return &rec, nil
}

func (r *depositRecordRepository) List(ctx context.Context, f DepositRecordFilter) ([]*model.DepositRecord, int64, error) {
	db := dbFromCtx(ctx, r.db).WithContext(ctx).Model(&model.DepositRecord{})

	if f.PlayerID != nil {
		db = db.Where("player_id = ?", *f.PlayerID)
	}
	if len(f.Status) > 0 {
		db = db.Where("status IN ?", f.Status)
	}
	if len(f.PaymentMethod) > 0 {
		db = db.Where("payment_method IN ?", f.PaymentMethod)
	}
	if f.StartDate != nil {
		db = db.Where("created_at >= ?", *f.StartDate)
	}
	if f.EndDate != nil {
		db = db.Where("created_at <= ?", *f.EndDate)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count deposit records: %w", err)
	}

	sortExpr, ok := depositSortMap[f.Sort]
	if !ok {
		sortExpr = "created_at DESC"
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	pageSize := f.PageSize
	if pageSize < 1 {
		pageSize = 20
	}

	var records []*model.DepositRecord
	if err := db.Order(sortExpr).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("list deposit records: %w", err)
	}

	return records, total, nil
}

func (r *depositRecordRepository) Update(ctx context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error) {
	updates := map[string]interface{}{}

	if input.NewStatus != nil {
		updates["status"] = *input.NewStatus
	}
	if input.InternalNote != nil {
		if *input.InternalNote == nil {
			updates["internal_note"] = nil
		} else {
			updates["internal_note"] = **input.InternalNote
		}
	}
	if input.DisplayNote != nil {
		if *input.DisplayNote == nil {
			updates["display_note"] = nil
		} else {
			updates["display_note"] = **input.DisplayNote
		}
	}

	if len(updates) == 0 {
		return r.FindByID(ctx, id)
	}

	result := dbFromCtx(ctx, r.db).WithContext(ctx).
		Model(&model.DepositRecord{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return nil, fmt.Errorf("update deposit record: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, apperr.ErrNotFound
	}

	return r.FindByID(ctx, id)
}

func (r *depositRecordRepository) ListByPlayer(ctx context.Context, playerID uuid.UUID, f PlayerDepositFilter) ([]*model.DepositRecord, int64, error) {
	db := dbFromCtx(ctx, r.db).WithContext(ctx).Model(&model.DepositRecord{}).Where("player_id = ?", playerID)

	if f.StartDate != nil {
		db = db.Where("created_at >= ?", *f.StartDate)
	}
	if f.EndDate != nil {
		db = db.Where("created_at <= ?", *f.EndDate)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count player deposit records: %w", err)
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	pageSize := f.PageSize
	if pageSize < 1 {
		pageSize = 20
	}

	var records []*model.DepositRecord
	if err := db.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("list player deposit records: %w", err)
	}

	return records, total, nil
}

func (r *depositRecordRepository) AggregateByPlayer(ctx context.Context, playerID uuid.UUID) (DepositAggregate, error) {
	var agg DepositAggregate

	// 唯讀 REPEATABLE READ 交易：兩條查詢取自同一快照（避免之間有寫入造成統計 / 時間不一致）。
	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// (1) 各幣別分桶統計
		var rows []CurrencyAggregate
		if err := tx.Model(&model.DepositRecord{}).
			Select(`currency,
				COUNT(*) FILTER (WHERE status = 'completed')                 AS completed_count,
				COALESCE(SUM(amount) FILTER (WHERE status = 'completed'), 0) AS completed_amount,
				COUNT(*) FILTER (WHERE status = 'refunded')                  AS refunded_count,
				COALESCE(SUM(amount) FILTER (WHERE status = 'refunded'), 0)  AS refunded_amount,
				COUNT(*) FILTER (WHERE status = 'failed')                    AS failed_count`).
			Where("player_id = ? AND status IN ?", playerID, reportableStatuses).
			Group("currency").
			Order("currency ASC").
			Scan(&rows).Error; err != nil {
			return err
		}

		// (2) 玩家層級首末次（成功口徑）＋ 生涯天數（UTC 日曆日差）
		var timeRow struct {
			FirstTopupAt *time.Time
			LastTopupAt  *time.Time
			LifetimeDays *int
		}
		if err := tx.Model(&model.DepositRecord{}).
			Select(`MIN(created_at) AS first_topup_at,
				MAX(created_at) AS last_topup_at,
				(MAX(created_at) AT TIME ZONE 'UTC')::date - (MIN(created_at) AT TIME ZONE 'UTC')::date AS lifetime_days`).
			Where("player_id = ? AND status IN ?", playerID, successStatuses).
			Scan(&timeRow).Error; err != nil {
			return err
		}

		agg.Totals = rows
		agg.FirstTopupAt = timeRow.FirstTopupAt
		agg.LastTopupAt = timeRow.LastTopupAt
		agg.LifetimeDays = timeRow.LifetimeDays
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})

	if txErr != nil {
		return DepositAggregate{}, fmt.Errorf("aggregate deposits by player: %w", txErr)
	}
	if agg.Totals == nil {
		agg.Totals = []CurrencyAggregate{}
	}
	return agg, nil
}

// ─── Fake ────────────────────────────────────────────────────────────────────

// FakeDepositRecordRepository 用於測試的 in-memory 實現。
type FakeDepositRecordRepository struct {
	records map[uuid.UUID]*model.DepositRecord
}

func NewFakeDepositRecordRepository() DepositRecordRepository {
	return &FakeDepositRecordRepository{records: make(map[uuid.UUID]*model.DepositRecord)}
}

func (r *FakeDepositRecordRepository) Create(_ context.Context, rec *model.DepositRecord) error {
	if rec.ID == (uuid.UUID{}) {
		rec.ID = uuid.New()
	}
	if rec.ReferenceNo != nil {
		for _, existing := range r.records {
			if existing.ReferenceNo != nil && *existing.ReferenceNo == *rec.ReferenceNo {
				return apperr.ErrReferenceNoConflict
			}
		}
	}
	r.records[rec.ID] = rec
	return nil
}

func (r *FakeDepositRecordRepository) FindByID(_ context.Context, id uuid.UUID) (*model.DepositRecord, error) {
	rec, ok := r.records[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return rec, nil
}

func (r *FakeDepositRecordRepository) List(_ context.Context, f DepositRecordFilter) ([]*model.DepositRecord, int64, error) {
	var result []*model.DepositRecord
	for _, rec := range r.records {
		result = append(result, rec)
	}
	return result, int64(len(result)), nil
}

func (r *FakeDepositRecordRepository) Update(_ context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error) {
	rec, ok := r.records[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	if input.NewStatus != nil {
		rec.Status = *input.NewStatus
	}
	if input.InternalNote != nil {
		rec.InternalNote = *input.InternalNote
	}
	if input.DisplayNote != nil {
		rec.DisplayNote = *input.DisplayNote
	}
	return rec, nil
}

func (r *FakeDepositRecordRepository) ListByPlayer(_ context.Context, playerID uuid.UUID, f PlayerDepositFilter) ([]*model.DepositRecord, int64, error) {
	var result []*model.DepositRecord
	for _, rec := range r.records {
		if rec.PlayerID == playerID {
			result = append(result, rec)
		}
	}
	return result, int64(len(result)), nil
}

func (r *FakeDepositRecordRepository) AggregateByPlayer(_ context.Context, playerID uuid.UUID) (DepositAggregate, error) {
	byCurrency := map[string]*CurrencyAggregate{}
	var firstAt, lastAt *time.Time

	for _, rec := range r.records {
		if rec.PlayerID != playerID {
			continue
		}
		switch rec.Status {
		case model.DepositStatusCompleted, model.DepositStatusRefunded, model.DepositStatusFailed:
			ca := byCurrency[rec.Currency]
			if ca == nil {
				ca = &CurrencyAggregate{Currency: rec.Currency}
				byCurrency[rec.Currency] = ca
			}
			switch rec.Status {
			case model.DepositStatusCompleted:
				ca.CompletedCount++
				ca.CompletedAmount += rec.Amount
			case model.DepositStatusRefunded:
				ca.RefundedCount++
				ca.RefundedAmount += rec.Amount
			case model.DepositStatusFailed:
				ca.FailedCount++
			}
		}
		// 成功口徑（completed ∪ refunded）才計入首末次。
		if rec.Status == model.DepositStatusCompleted || rec.Status == model.DepositStatusRefunded {
			t := rec.CreatedAt
			if firstAt == nil || t.Before(*firstAt) {
				v := t
				firstAt = &v
			}
			if lastAt == nil || t.After(*lastAt) {
				v := t
				lastAt = &v
			}
		}
	}

	currencies := make([]string, 0, len(byCurrency))
	for c := range byCurrency {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	totals := make([]CurrencyAggregate, 0, len(currencies))
	for _, c := range currencies {
		totals = append(totals, *byCurrency[c])
	}

	agg := DepositAggregate{Totals: totals, FirstTopupAt: firstAt, LastTopupAt: lastAt}
	if firstAt != nil && lastAt != nil {
		days := utcCalendarDayDiff(*firstAt, *lastAt)
		agg.LifetimeDays = &days
	}
	return agg, nil
}

// utcCalendarDayDiff 回傳 last 與 first 的 UTC 日曆日差（DATE(last)-DATE(first)），與真實 SQL 口徑一致。
func utcCalendarDayDiff(first, last time.Time) int {
	fy, fm, fd := first.UTC().Date()
	ly, lm, ld := last.UTC().Date()
	fDate := time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC)
	lDate := time.Date(ly, lm, ld, 0, 0, 0, 0, time.UTC)
	return int(lDate.Sub(fDate).Hours() / 24)
}
