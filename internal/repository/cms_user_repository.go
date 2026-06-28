package repository

import (
	"context"
	"errors"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

// CMSUserRepository 定义 CMS 用户仓储接口。
type CMSUserRepository interface {
	FindByUsername(ctx context.Context, username string) (*model.CMSUser, error)
	Create(ctx context.Context, u *model.CMSUser) error
}

type cmsUserRepository struct {
	db *gorm.DB
}

// NewCMSUserRepository 创建 CMS 用户仓储。
func NewCMSUserRepository(db *gorm.DB) CMSUserRepository {
	return &cmsUserRepository{db: db}
}

// FindByUsername 按用户名查找。
func (r *cmsUserRepository) FindByUsername(ctx context.Context, username string) (*model.CMSUser, error) {
	var user model.CMSUser
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

// Create 创建新用户。
func (r *cmsUserRepository) Create(ctx context.Context, u *model.CMSUser) error {
	if err := r.db.WithContext(ctx).Create(u).Error; err != nil {
		// PostgreSQL unique constraint violation = 23505
		if err.Error() == "ERROR: duplicate key value violates unique constraint \"uq_cms_users_username\" (SQLSTATE 23505)" {
			return apperr.ErrConflict
		}
		return err
	}
	return nil
}

// FakeCMSUserRepository 用于测试的 fake 实现。
type FakeCMSUserRepository struct {
	users map[string]*model.CMSUser
}

// NewFakeCMSUserRepository 创建 fake CMSUserRepository。
func NewFakeCMSUserRepository() CMSUserRepository {
	return &FakeCMSUserRepository{
		users: make(map[string]*model.CMSUser),
	}
}

// FindByUsername fake 实现。
func (r *FakeCMSUserRepository) FindByUsername(ctx context.Context, username string) (*model.CMSUser, error) {
	if u, ok := r.users[username]; ok {
		return u, nil
	}
	return nil, apperr.ErrNotFound
}

// Create fake 实现。
func (r *FakeCMSUserRepository) Create(ctx context.Context, u *model.CMSUser) error {
	if _, ok := r.users[u.Username]; ok {
		return apperr.ErrConflict
	}
	r.users[u.Username] = u
	return nil
}
