package repository

import (
	"context"
	"errors"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

// MemberRepository 定义玩家仓储接口。
// Member 注册现阶段不开放，仅提供查询。
type MemberRepository interface {
	FindByUsername(ctx context.Context, username string) (*model.Member, error)
}

type memberRepository struct {
	db *gorm.DB
}

// NewMemberRepository 创建玩家仓储。
func NewMemberRepository(db *gorm.DB) MemberRepository {
	return &memberRepository{db: db}
}

// FindByUsername 按用户名查找。
func (r *memberRepository) FindByUsername(ctx context.Context, username string) (*model.Member, error) {
	var member model.Member
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, err
	}
	return &member, nil
}

// FakeMemberRepository 用于测试的 fake 实现。
type FakeMemberRepository struct {
	members map[string]*model.Member
}

// NewFakeMemberRepository 创建 fake MemberRepository。
func NewFakeMemberRepository() MemberRepository {
	return &FakeMemberRepository{
		members: make(map[string]*model.Member),
	}
}

// FindByUsername fake 实现。
func (r *FakeMemberRepository) FindByUsername(ctx context.Context, username string) (*model.Member, error) {
	if m, ok := r.members[username]; ok {
		return m, nil
	}
	return nil, apperr.ErrNotFound
}
