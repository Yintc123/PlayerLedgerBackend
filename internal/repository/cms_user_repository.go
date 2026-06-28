package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListCMSUsersOptions CMS users 列表篩選條件（cms-users-api §4.1 / §9）。
// handler 驗證 Sort / RoleFilter 白名單與 page 邊界後傳入；repository 負責 username_like
// 的 SQL LIKE escape（§4.1）與分頁。
type ListCMSUsersOptions struct {
	Page           int
	PageSize       int
	RoleFilter     []string // 空 = 不篩；每個值已由 handler 驗證在 [admin, user, viewer]
	UsernameLike   string   // 空 = 不篩；repository 內 escape 後前後加 % 餵給 ILIKE
	IncludeDeleted bool     // true 才回傳已軟刪除紀錄（handler 已做 admin 權限 gate）
	Sort           string   // 白名單：created_at / -created_at / username / -username
}

// CMSUserPatch 部分更新欄位（cms-users-api §10）。nil = 不改。
type CMSUserPatch struct {
	Username     *string
	Role         *string
	PasswordHash *string // self-update 改密碼用
}

// CMSUserRepository 定義 CMS 用戶倉儲介面（cms-users-api §10）。
// FindByUsername：找不到回 apperr.ErrNotFound；DB 錯誤一律 fmt.Errorf("find cms user: %w", err)。
// Create：username 已存在回 apperr.ErrConflict（依規格 §12.5 unique constraint 23505 包裝）。
type CMSUserRepository interface {
	// 既有（由 /auth/register 與 /auth/login 使用）
	FindByUsername(ctx context.Context, username string) (*model.CMSUser, error)
	Create(ctx context.Context, u *model.CMSUser) error

	// 新增（cms-users-api 用）
	// FindByID：includeDeleted=false 時軟刪除視為不存在（回 ErrNotFound）。
	// 在 transaction 內呼叫時自動加 SELECT … FOR UPDATE（§10.2）。
	FindByID(ctx context.Context, id string, includeDeleted bool) (*model.CMSUser, error)
	List(ctx context.Context, opts ListCMSUsersOptions) ([]model.CMSUser, int64, error)
	// Update：username 衝突回 apperr.ErrConflict；目標不存在回 apperr.ErrNotFound。
	Update(ctx context.Context, id string, patch CMSUserPatch) error
	// SoftDelete：受影響列 = 0（不存在或已軟刪）回 apperr.ErrNotFound。
	SoftDelete(ctx context.Context, id string) error
	// CountActiveAdmins：未軟刪除且 role=admin 的數量（INV-1 用）。
	CountActiveAdmins(ctx context.Context) (int64, error)
}

// cmsUserSortMap 白名單映射，防止 SQL injection（§4.1）。
var cmsUserSortMap = map[string]string{
	"-created_at": "created_at DESC",
	"created_at":  "created_at ASC",
	"-username":   "username DESC",
	"username":    "username ASC",
}

type cmsUserRepository struct {
	db *gorm.DB
}

// NewCMSUserRepository 創建 CMS 用戶倉儲。
func NewCMSUserRepository(db *gorm.DB) CMSUserRepository {
	return &cmsUserRepository{db: db}
}

// FindByUsername 按用戶名查找（排除軟刪除）。
func (r *cmsUserRepository) FindByUsername(ctx context.Context, username string) (*model.CMSUser, error) {
	var user model.CMSUser
	if err := dbFromCtx(ctx, r.db).WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("find cms user: %w", err)
	}
	return &user, nil
}

// Create 創建新用戶。
func (r *cmsUserRepository) Create(ctx context.Context, u *model.CMSUser) error {
	if err := dbFromCtx(ctx, r.db).WithContext(ctx).Create(u).Error; err != nil {
		// PostgreSQL unique constraint violation = 23505
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return apperr.ErrConflict
		}
		return fmt.Errorf("create cms user: %w", err)
	}
	return nil
}

// FindByID 按 ID 查找。
// 註：INV-1 的並發保護不在此（鎖單一 target 列會與 CountActiveAdmins 的 admin-set 鎖
// 造成 lock-order 反轉而 deadlock）；改由 CountActiveAdmins 以固定順序鎖整個 admin set
// 統一序列化（見該方法說明）。
func (r *cmsUserRepository) FindByID(ctx context.Context, id string, includeDeleted bool) (*model.CMSUser, error) {
	db := dbFromCtx(ctx, r.db).WithContext(ctx)
	if includeDeleted {
		db = db.Unscoped()
	}

	var user model.CMSUser
	if err := db.Where("id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("find cms user by id: %w", err)
	}
	return &user, nil
}

// List 分頁列出 CMS users（§4.1）。
func (r *cmsUserRepository) List(ctx context.Context, opts ListCMSUsersOptions) ([]model.CMSUser, int64, error) {
	db := dbFromCtx(ctx, r.db).WithContext(ctx).Model(&model.CMSUser{})

	if opts.IncludeDeleted {
		db = db.Unscoped()
	}
	if len(opts.RoleFilter) > 0 {
		db = db.Where("role IN ?", opts.RoleFilter)
	}
	if opts.UsernameLike != "" {
		// escape SQL LIKE 特殊字元（\ % _），避免 caller 用 % 全表掃描或 _ 繞過 minLength（§4.1）。
		pattern := "%" + escapeLike(opts.UsernameLike) + "%"
		db = db.Where(`username ILIKE ? ESCAPE '\'`, pattern)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count cms users: %w", err)
	}

	sortExpr, ok := cmsUserSortMap[opts.Sort]
	if !ok {
		sortExpr = "created_at DESC"
	}

	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}

	var users []model.CMSUser
	if err := db.Order(sortExpr).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("list cms users: %w", err)
	}

	return users, total, nil
}

// Update 部分更新 username / role / password_hash（§10）。
func (r *cmsUserRepository) Update(ctx context.Context, id string, patch CMSUserPatch) error {
	updates := map[string]interface{}{}
	if patch.Username != nil {
		updates["username"] = *patch.Username
	}
	if patch.Role != nil {
		updates["role"] = *patch.Role
	}
	if patch.PasswordHash != nil {
		updates["password_hash"] = *patch.PasswordHash
	}
	if len(updates) == 0 {
		return nil
	}

	result := dbFromCtx(ctx, r.db).WithContext(ctx).
		Model(&model.CMSUser{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		var pgErr *pgconn.PgError
		if errors.As(result.Error, &pgErr) && pgErr.Code == "23505" {
			return apperr.ErrConflict
		}
		return fmt.Errorf("update cms user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// SoftDelete 軟刪除（§4.4）。GORM 自動加 deleted_at IS NULL 條件，
// 重複刪除（已軟刪）受影響列 = 0 → ErrNotFound。
func (r *cmsUserRepository) SoftDelete(ctx context.Context, id string) error {
	result := dbFromCtx(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).
		Delete(&model.CMSUser{})
	if result.Error != nil {
		return fmt.Errorf("soft delete cms user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// CountActiveAdmins 計算未軟刪除的 admin 數量（INV-1）。
// 在 transaction 內時以 SELECT … ORDER BY id FOR UPDATE 鎖住所有 admin 列：
//   - 序列化並發的 admin 降級 / 刪除（§6 註：兩 caller 同時降級彼此的 race 保護）；
//   - 固定 ORDER BY id 確保所有 caller 以相同順序取鎖，避免 deadlock。
func (r *cmsUserRepository) CountActiveAdmins(ctx context.Context) (int64, error) {
	db := dbFromCtx(ctx, r.db).WithContext(ctx).Model(&model.CMSUser{}).Where("role = ?", "admin")

	if hasTx(ctx) {
		var admins []model.CMSUser
		if err := db.Order("id").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&admins).Error; err != nil {
			return 0, fmt.Errorf("count active admins: %w", err)
		}
		return int64(len(admins)), nil
	}

	var count int64
	if err := db.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return count, nil
}

// escapeLike 跳脫 SQL LIKE/ILIKE 的特殊字元（配合 ESCAPE '\' 子句）。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ─── Fake ────────────────────────────────────────────────────────────────────

// FakeCMSUserRepository 用於測試的 in-memory 實現，支援完整 CMS users 操作。
// 以 ID 為主鍵；軟刪除以 model.Base.DeletedAt 標記。
type FakeCMSUserRepository struct {
	users map[uuid.UUID]*model.CMSUser
}

// NewFakeCMSUserRepository 創建 fake CMSUserRepository。
func NewFakeCMSUserRepository() CMSUserRepository {
	return &FakeCMSUserRepository{
		users: make(map[uuid.UUID]*model.CMSUser),
	}
}

func (r *FakeCMSUserRepository) isDeleted(u *model.CMSUser) bool {
	return u.DeletedAt.Valid
}

func (r *FakeCMSUserRepository) FindByUsername(_ context.Context, username string) (*model.CMSUser, error) {
	for _, u := range r.users {
		if u.Username == username && !r.isDeleted(u) {
			return u, nil
		}
	}
	return nil, apperr.ErrNotFound
}

func (r *FakeCMSUserRepository) Create(_ context.Context, u *model.CMSUser) error {
	for _, existing := range r.users {
		if existing.Username == u.Username && !r.isDeleted(existing) {
			return apperr.ErrConflict
		}
	}
	if u.ID == (uuid.UUID{}) {
		u.ID = uuid.New()
	}
	r.users[u.ID] = u
	return nil
}

func (r *FakeCMSUserRepository) FindByID(_ context.Context, id string, includeDeleted bool) (*model.CMSUser, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, apperr.ErrNotFound
	}
	u, ok := r.users[uid]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	if r.isDeleted(u) && !includeDeleted {
		return nil, apperr.ErrNotFound
	}
	return u, nil
}

func (r *FakeCMSUserRepository) List(_ context.Context, opts ListCMSUsersOptions) ([]model.CMSUser, int64, error) {
	roleSet := map[string]bool{}
	for _, role := range opts.RoleFilter {
		roleSet[role] = true
	}

	var matched []model.CMSUser
	for _, u := range r.users {
		if r.isDeleted(u) && !opts.IncludeDeleted {
			continue
		}
		if len(roleSet) > 0 && !roleSet[u.Role] {
			continue
		}
		if opts.UsernameLike != "" &&
			!strings.Contains(strings.ToLower(u.Username), strings.ToLower(opts.UsernameLike)) {
			continue
		}
		matched = append(matched, *u)
	}

	sortFakeCMSUsers(matched, opts.Sort)

	total := int64(len(matched))

	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	if start >= len(matched) {
		return []model.CMSUser{}, total, nil
	}
	end := start + pageSize
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], total, nil
}

func sortFakeCMSUsers(users []model.CMSUser, sortKey string) {
	switch sortKey {
	case "username":
		sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	case "-username":
		sort.Slice(users, func(i, j int) bool { return users[i].Username > users[j].Username })
	case "created_at":
		sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.Before(users[j].CreatedAt) })
	default: // -created_at
		sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.After(users[j].CreatedAt) })
	}
}

func (r *FakeCMSUserRepository) Update(_ context.Context, id string, patch CMSUserPatch) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return apperr.ErrNotFound
	}
	u, ok := r.users[uid]
	if !ok || r.isDeleted(u) {
		return apperr.ErrNotFound
	}
	if patch.Username != nil {
		for _, other := range r.users {
			if other.ID != uid && other.Username == *patch.Username && !r.isDeleted(other) {
				return apperr.ErrConflict
			}
		}
		u.Username = *patch.Username
	}
	if patch.Role != nil {
		u.Role = *patch.Role
	}
	if patch.PasswordHash != nil {
		u.PasswordHash = *patch.PasswordHash
	}
	return nil
}

func (r *FakeCMSUserRepository) SoftDelete(_ context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return apperr.ErrNotFound
	}
	u, ok := r.users[uid]
	if !ok || r.isDeleted(u) {
		return apperr.ErrNotFound
	}
	u.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	return nil
}

func (r *FakeCMSUserRepository) CountActiveAdmins(_ context.Context) (int64, error) {
	var count int64
	for _, u := range r.users {
		if u.Role == "admin" && !r.isDeleted(u) {
			count++
		}
	}
	return count, nil
}
