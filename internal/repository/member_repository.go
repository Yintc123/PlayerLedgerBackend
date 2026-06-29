package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

// Keyset 表示 keyset 分頁的續查位置（上一頁最後一列）。
type Keyset struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// PlayerSearchFilter 玩家搜尋條件（players-model.md §8）。
// 所有指標欄位 nil 表示「未提供該條件」；至少需一個搜尋條件非 nil（API 層保證）。
// 各字串欄位已由 API 層完成 trim / lowercase / 正規化，repository 直接使用。
type PlayerSearchFilter struct {
	PlayerID          *uuid.UUID // 精確
	ExternalID        *string    // 精確
	DisplayNamePrefix *string    // 前綴（lower + LIKE）
	EmailPrefix       *string    // 前綴（已 lowercase）
	Phone             *string    // 精確（已正規化）

	After *Keyset // nil = 第一頁；非 nil = 續查 (created_at, id) < After
	Limit int     // repository 忠實照此值；service 已設為 pageSize+1 以判斷 hasMore
}

// MemberRepository 定義玩家倉儲介面。
// FindByUsername/FindByID：找不到回 apperr.ErrNotFound；DB 錯誤一律 fmt.Errorf 包裝。
// Member 註冊現階段不開放，僅提供查詢，故不提供 Create 方法。
type MemberRepository interface {
	FindByUsername(ctx context.Context, username string) (*model.Member, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.Member, error)
	// Search 依 filter 查詢：AND 組合、ORDER BY created_at DESC, id DESC、deleted_at IS NULL、
	// 套用 After keyset 條件、LIMIT filter.Limit。忠實回傳 ≤ filter.Limit 筆；
	// hasMore 判斷與 next_cursor 編碼由 service 處理（players-api.md §6）。
	Search(ctx context.Context, f PlayerSearchFilter) ([]*model.Member, error)
}

type memberRepository struct {
	db *gorm.DB
}

// NewMemberRepository 創建玩家倉儲。
func NewMemberRepository(db *gorm.DB) MemberRepository {
	return &memberRepository{db: db}
}

// FindByUsername 按用戶名查找。
func (r *memberRepository) FindByUsername(ctx context.Context, username string) (*model.Member, error) {
	var member model.Member
	if err := dbFromCtx(ctx, r.db).WithContext(ctx).Where("username = ?", username).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("find member: %w", err)
	}
	return &member, nil
}

// FindByID 按 UUID 查找。
func (r *memberRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.Member, error) {
	var member model.Member
	if err := dbFromCtx(ctx, r.db).WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("find member by id: %w", err)
	}
	return &member, nil
}

// Search 玩家搜尋（keyset 分頁）。soft-delete 由 GORM 對 gorm.DeletedAt 自動加 deleted_at IS NULL。
func (r *memberRepository) Search(ctx context.Context, f PlayerSearchFilter) ([]*model.Member, error) {
	db := dbFromCtx(ctx, r.db).WithContext(ctx).Model(&model.Member{})

	if f.PlayerID != nil {
		db = db.Where("id = ?", *f.PlayerID)
	}
	if f.ExternalID != nil {
		db = db.Where("external_id = ?", *f.ExternalID)
	}
	if f.DisplayNamePrefix != nil {
		db = db.Where("lower(display_name) LIKE ?", escapeLikePrefix(strings.ToLower(*f.DisplayNamePrefix)))
	}
	if f.EmailPrefix != nil {
		// EmailPrefix 已由 handler lowercase
		db = db.Where("lower(email) LIKE ?", escapeLikePrefix(*f.EmailPrefix))
	}
	if f.Phone != nil {
		db = db.Where("phone = ?", *f.Phone)
	}
	if f.After != nil {
		// row-value 比較配合 ORDER BY created_at DESC, id DESC
		db = db.Where("(created_at, id) < (?, ?)", f.After.CreatedAt, f.After.ID)
	}

	limit := f.Limit
	if limit < 1 {
		limit = 1
	}

	var members []*model.Member
	if err := db.Order("created_at DESC, id DESC").Limit(limit).Find(&members).Error; err != nil {
		return nil, fmt.Errorf("search members: %w", err)
	}
	return members, nil
}

// escapeLikePrefix 跳脫使用者輸入中的 LIKE 萬用字元（\ % _），再附加 % 形成前綴 pattern。
// PostgreSQL LIKE 預設以 \ 為跳脫字元，故無需 ESCAPE 子句。
func escapeLikePrefix(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s) + "%"
}

// ─── Fake ────────────────────────────────────────────────────────────────────

// FakeMemberRepository 用於測試的 fake 實現。
type FakeMemberRepository struct {
	members map[string]*model.Member
}

// NewFakeMemberRepository 創建 fake MemberRepository。
func NewFakeMemberRepository() MemberRepository {
	return &FakeMemberRepository{
		members: make(map[string]*model.Member),
	}
}

// FindByUsername fake 實現。
func (r *FakeMemberRepository) FindByUsername(ctx context.Context, username string) (*model.Member, error) {
	if m, ok := r.members[username]; ok {
		return m, nil
	}
	return nil, apperr.ErrNotFound
}

// FindByID fake 實現（按 ID 查找）。
func (r *FakeMemberRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.Member, error) {
	for _, m := range r.members {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, apperr.ErrNotFound
}

// Search fake 實現：套用條件、created_at DESC / id DESC 排序、After keyset、Limit。
func (r *FakeMemberRepository) Search(_ context.Context, f PlayerSearchFilter) ([]*model.Member, error) {
	var matched []*model.Member
	for _, m := range r.members {
		if !fakeMemberMatches(m, f) {
			continue
		}
		matched = append(matched, m)
	}

	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID.String() > matched[j].ID.String()
	})

	if f.After != nil {
		filtered := matched[:0]
		for _, m := range matched {
			if keysetBefore(m, *f.After) {
				filtered = append(filtered, m)
			}
		}
		matched = filtered
	}

	if f.Limit > 0 && len(matched) > f.Limit {
		matched = matched[:f.Limit]
	}
	return matched, nil
}

func fakeMemberMatches(m *model.Member, f PlayerSearchFilter) bool {
	if m.DeletedAt.Valid {
		return false
	}
	if f.PlayerID != nil && m.ID != *f.PlayerID {
		return false
	}
	if f.ExternalID != nil && (m.ExternalID == nil || *m.ExternalID != *f.ExternalID) {
		return false
	}
	if f.DisplayNamePrefix != nil &&
		!strings.HasPrefix(strings.ToLower(m.DisplayName), strings.ToLower(*f.DisplayNamePrefix)) {
		return false
	}
	if f.EmailPrefix != nil &&
		(m.Email == nil || !strings.HasPrefix(strings.ToLower(*m.Email), *f.EmailPrefix)) {
		return false
	}
	if f.Phone != nil && (m.Phone == nil || *m.Phone != *f.Phone) {
		return false
	}
	return true
}

// keysetBefore 回報 m 是否排在 after 之後（created_at DESC, id DESC 下的「後面」= 值較小）。
func keysetBefore(m *model.Member, after Keyset) bool {
	if !m.CreatedAt.Equal(after.CreatedAt) {
		return m.CreatedAt.Before(after.CreatedAt)
	}
	return m.ID.String() < after.ID.String()
}
