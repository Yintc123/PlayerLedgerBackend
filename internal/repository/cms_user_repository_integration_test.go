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

// TestCMSUserRepository_FindByUsername_存在_回傳用戶
func TestCMSUserRepository_FindByUsername_存在_回傳用戶(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 準備：建立用戶
	user := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "testuser",
		PasswordHash: "$2a$12$hash",
		Role:         "user",
	}
	err := db.Create(user).Error
	require.NoError(t, err)

	// 執行
	found, err := repo.FindByUsername(ctx, "testuser")

	// 驗證
	assert.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "testuser", found.Username)
	assert.Equal(t, "$2a$12$hash", found.PasswordHash)
	assert.Equal(t, "user", found.Role)
}

// TestCMSUserRepository_FindByUsername_不存在_回傳ErrNotFound
func TestCMSUserRepository_FindByUsername_不存在_回傳ErrNotFound(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 執行
	found, err := repo.FindByUsername(ctx, "nonexistent")

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Nil(t, found)
}

// TestCMSUserRepository_FindByUsername_軟刪_視為不存在
func TestCMSUserRepository_FindByUsername_軟刪_視為不存在(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 準備：建立用戶並軟刪
	user := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "deleteduser",
		PasswordHash: "$2a$12$hash",
		Role:         "user",
	}
	err := db.Create(user).Error
	require.NoError(t, err)

	err = db.Delete(user).Error
	require.NoError(t, err)

	// 執行
	found, err := repo.FindByUsername(ctx, "deleteduser")

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Nil(t, found)
}

// TestCMSUserRepository_Create_成功_新增用戶
func TestCMSUserRepository_Create_成功_新增用戶(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 準備
	user := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "newuser",
		PasswordHash: "$2a$12$newhash",
		Role:         "admin",
	}

	// 執行
	err := repo.Create(ctx, user)

	// 驗證
	assert.NoError(t, err)

	// 驗證數據已寫入
	var found model.CMSUser
	dbErr := db.Where("username = ?", "newuser").First(&found).Error
	assert.NoError(t, dbErr)
	assert.Equal(t, "newuser", found.Username)
	assert.Equal(t, "admin", found.Role)
}

// TestCMSUserRepository_Create_用戶名已存在_回傳ErrConflict
func TestCMSUserRepository_Create_用戶名已存在_回傳ErrConflict(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 準備：建立第一個用戶
	user1 := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "duplicate",
		PasswordHash: "$2a$12$hash1",
		Role:         "user",
	}
	err := db.Create(user1).Error
	require.NoError(t, err)

	// 執行：嘗試建立相同用戶名的用戶
	user2 := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "duplicate",
		PasswordHash: "$2a$12$hash2",
		Role:         "admin",
	}
	err = repo.Create(ctx, user2)

	// 驗證
	assert.ErrorIs(t, err, apperr.ErrConflict)
}

// TestCMSUserRepository_Create_軟刪後可重用用戶名
func TestCMSUserRepository_Create_軟刪後可重用用戶名(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 準備：建立並軟刪用戶
	user1 := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "reusable",
		PasswordHash: "$2a$12$hash1",
		Role:         "user",
	}
	err := db.Create(user1).Error
	require.NoError(t, err)

	err = db.Delete(user1).Error
	require.NoError(t, err)

	// 執行：用相同用戶名建立新用戶
	user2 := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "reusable",
		PasswordHash: "$2a$12$hash2",
		Role:         "admin",
	}
	err = repo.Create(ctx, user2)

	// 驗證
	assert.NoError(t, err)
	found, err := repo.FindByUsername(ctx, "reusable")
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, user2.ID, found.ID)
}

// TestCMSUserRepository_Create_DB錯誤_回傳包裝錯誤
func TestCMSUserRepository_Create_DB錯誤_回傳包裝錯誤(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	// 準備：無效 role 值觸發 CHECK constraint violation（SQLSTATE 23514），
	// 不是 23505 unique violation → 不被對應到 ErrConflict，走包裝錯誤路徑。
	// 注意：空字串 PasswordHash 不觸發 NOT NULL（PostgreSQL 只攔截 NULL）。
	user := &model.CMSUser{
		Base: model.Base{
			ID: uuid.New(),
		},
		Username:     "validuser",
		PasswordHash: "somehash",
		Role:         "invalid_role", // violates CHECK (role IN ('admin','user','viewer'))
	}

	// 執行
	err := repo.Create(ctx, user)

	// 驗證：應該得到一個包裝過的錯誤，不是 ErrConflict
	require.Error(t, err) // require.Error：失敗時立刻停止，避免後續 err.Error() panic
	assert.NotEqual(t, apperr.ErrConflict, err)
	assert.Contains(t, err.Error(), "create cms user:")
}

// TestCMSUserRepository_FindByUsername_DB錯誤_回傳包裝錯誤
func TestCMSUserRepository_FindByUsername_DB錯誤_回傳包裝錯誤(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)

	// 用已取消的 context 強制 DB 查詢錯誤。
	// 不可用 sqlDB.Close()：db.DB() 取得的是共享的 testDB 連線池，
	// 關閉它會破壞同 package 其他測試的隔離（database is closed）。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 執行
	_, err := repo.FindByUsername(ctx, "test")

	// 驗證
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "find cms user:")
}
