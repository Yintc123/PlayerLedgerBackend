//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
)

// TestMemberRepository_FindByUsername_存在_回傳會員
func TestMemberRepository_FindByUsername_存在_回傳會員(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	// 準備：建立會員
	member := &model.Member{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "player1",
		PasswordHash: "$2a$12$playerhash",
	}
	err := db.Create(member).Error
	require.NoError(t, err)

	// 執行
	found, err := repo.FindByUsername(ctx, "player1")

	// 驗證
	assert.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "player1", found.Username)
	assert.Equal(t, "$2a$12$playerhash", found.PasswordHash)
	assert.Equal(t, member.ID, found.ID)
}

// TestMemberRepository_FindByUsername_不存在_回傳ErrNotFound
func TestMemberRepository_FindByUsername_不存在_回傳ErrNotFound(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	// 執行
	found, err := repo.FindByUsername(ctx, "nonexistentmember")

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Nil(t, found)
}

// TestMemberRepository_FindByUsername_軟刪_視為不存在
func TestMemberRepository_FindByUsername_軟刪_視為不存在(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	// 準備：建立會員並軟刪
	member := &model.Member{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "deletedmember",
		PasswordHash: "$2a$12$hash",
	}
	err := db.Create(member).Error
	require.NoError(t, err)

	err = db.Delete(member).Error
	require.NoError(t, err)

	// 執行
	found, err := repo.FindByUsername(ctx, "deletedmember")

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Nil(t, found)
}

// TestMemberRepository_FindByUsername_多個會員_返回正確的
func TestMemberRepository_FindByUsername_多個會員_返回正確的(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	// 準備：建立多個會員
	member1 := &model.Member{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "player1",
		PasswordHash: "$2a$12$hash1",
	}
	member2 := &model.Member{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "player2",
		PasswordHash: "$2a$12$hash2",
	}
	err := db.Create(member1).Error
	require.NoError(t, err)
	err = db.Create(member2).Error
	require.NoError(t, err)

	// 執行：查詢第一個會員
	found, err := repo.FindByUsername(ctx, "player1")

	// 驗證
	assert.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "player1", found.Username)
	assert.Equal(t, member1.ID, found.ID)

	// 執行：查詢第二個會員
	found, err = repo.FindByUsername(ctx, "player2")

	// 驗證
	assert.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "player2", found.Username)
	assert.Equal(t, member2.ID, found.ID)
}

// TestMemberRepository_FindByUsername_DB錯誤_回傳包裝錯誤
func TestMemberRepository_FindByUsername_DB錯誤_回傳包裝錯誤(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)

	// 用已取消的 context 強制 DB 查詢錯誤。
	// 不可用 sqlDB.Close()：db.DB() 取得的是共享的 testDB 連線池，
	// 關閉它會破壞同 package 其他測試的隔離（database is closed）。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 執行
	_, err := repo.FindByUsername(ctx, "test")

	// 驗證
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "find member:")
}
