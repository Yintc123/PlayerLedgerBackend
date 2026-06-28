package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
)

// TestFakeCMSUserRepository_FindByUsername_存在_回傳用戶
func TestFakeCMSUserRepository_FindByUsername_存在_回傳用戶(t *testing.T) {
	t.Parallel()
	repo := NewFakeCMSUserRepository()
	ctx := context.Background()

	// 準備
	user := &model.CMSUser{
		Base: model.Base{ID: uuid.New()},
		Username:     "fakeuser",
		PasswordHash: "$2a$12$fakehash",
		Role:         "user",
	}
	err := repo.Create(ctx, user)
	assert.NoError(t, err)

	// 執行
	found, err := repo.FindByUsername(ctx, "fakeuser")

	// 驗證
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, "fakeuser", found.Username)
	assert.Equal(t, "$2a$12$fakehash", found.PasswordHash)
}

// TestFakeCMSUserRepository_FindByUsername_不存在_回傳ErrNotFound
func TestFakeCMSUserRepository_FindByUsername_不存在_回傳ErrNotFound(t *testing.T) {
	t.Parallel()
	repo := NewFakeCMSUserRepository()
	ctx := context.Background()

	// 執行
	found, err := repo.FindByUsername(ctx, "nonexistent")

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Nil(t, found)
}

// TestFakeCMSUserRepository_Create_成功_新增用戶
func TestFakeCMSUserRepository_Create_成功_新增用戶(t *testing.T) {
	t.Parallel()
	repo := NewFakeCMSUserRepository()
	ctx := context.Background()

	// 準備
	user := &model.CMSUser{
		Base: model.Base{ID: uuid.New()},
		Username:     "newuser",
		PasswordHash: "$2a$12$newhash",
		Role:         "admin",
	}

	// 執行
	err := repo.Create(ctx, user)

	// 驗證
	assert.NoError(t, err)
	found, err := repo.FindByUsername(ctx, "newuser")
	assert.NoError(t, err)
	assert.NotNil(t, found)
}

// TestFakeCMSUserRepository_Create_用戶名已存在_回傳ErrConflict
func TestFakeCMSUserRepository_Create_用戶名已存在_回傳ErrConflict(t *testing.T) {
	t.Parallel()
	repo := NewFakeCMSUserRepository()
	ctx := context.Background()

	// 準備
	user1 := &model.CMSUser{
		Base: model.Base{ID: uuid.New()},
		Username:     "duplicate",
		PasswordHash: "$2a$12$hash1",
		Role:         "user",
	}
	err := repo.Create(ctx, user1)
	assert.NoError(t, err)

	// 執行
	user2 := &model.CMSUser{
		Base: model.Base{ID: uuid.New()},
		Username:     "duplicate",
		PasswordHash: "$2a$12$hash2",
		Role:         "admin",
	}
	err = repo.Create(ctx, user2)

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrConflict)
}

// TestFakeMemberRepository_FindByUsername_存在_回傳會員
func TestFakeMemberRepository_FindByUsername_存在_回傳會員(t *testing.T) {
	t.Parallel()
	repo := NewFakeMemberRepository()
	ctx := context.Background()

	// 準備
	member := &model.Member{
		Base: model.Base{ID: uuid.New()},
		Username:     "fakeplayer",
		PasswordHash: "$2a$12$playerhash",
	}
	// 直接注入到 fake store
	fakeRepo := repo.(*FakeMemberRepository)
	fakeRepo.members["fakeplayer"] = member

	// 執行
	found, err := repo.FindByUsername(ctx, "fakeplayer")

	// 驗證
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, "fakeplayer", found.Username)
}

// TestFakeMemberRepository_FindByUsername_不存在_回傳ErrNotFound
func TestFakeMemberRepository_FindByUsername_不存在_回傳ErrNotFound(t *testing.T) {
	t.Parallel()
	repo := NewFakeMemberRepository()
	ctx := context.Background()

	// 執行
	found, err := repo.FindByUsername(ctx, "nonexistentplayer")

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Nil(t, found)
}
