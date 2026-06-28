package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

// MemberRepository 定義玩家倉儲介面。
// FindByUsername：找不到回 apperr.ErrNotFound；DB 錯誤一律 fmt.Errorf("find member: %w", err)。
// Member 註冊現階段不開放，僅提供查詢，故不提供 Create 方法。
type MemberRepository interface {
	FindByUsername(ctx context.Context, username string) (*model.Member, error)
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
