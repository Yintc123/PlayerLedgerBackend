package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

// CMSUserRepository 定義 CMS 用戶倉儲介面。
// FindByUsername：找不到回 apperr.ErrNotFound；DB 錯誤一律 fmt.Errorf("find cms user: %w", err)。
// Create：username 已存在回 apperr.ErrConflict（依規格 §12.5 unique constraint 23505 包裝）。
type CMSUserRepository interface {
	FindByUsername(ctx context.Context, username string) (*model.CMSUser, error)
	Create(ctx context.Context, u *model.CMSUser) error
}

type cmsUserRepository struct {
	db *gorm.DB
}

// NewCMSUserRepository 創建 CMS 用戶倉儲。
func NewCMSUserRepository(db *gorm.DB) CMSUserRepository {
	return &cmsUserRepository{db: db}
}

// FindByUsername 按用戶名查找。
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

// FakeCMSUserRepository 用於測試的 fake 實現。
type FakeCMSUserRepository struct {
	users map[string]*model.CMSUser
}

// NewFakeCMSUserRepository 創建 fake CMSUserRepository。
func NewFakeCMSUserRepository() CMSUserRepository {
	return &FakeCMSUserRepository{
		users: make(map[string]*model.CMSUser),
	}
}

// FindByUsername fake 實現。
func (r *FakeCMSUserRepository) FindByUsername(ctx context.Context, username string) (*model.CMSUser, error) {
	if u, ok := r.users[username]; ok {
		return u, nil
	}
	return nil, apperr.ErrNotFound
}

// Create fake 實現。
func (r *FakeCMSUserRepository) Create(ctx context.Context, u *model.CMSUser) error {
	if _, ok := r.users[u.Username]; ok {
		return apperr.ErrConflict
	}
	r.users[u.Username] = u
	return nil
}
