//go:build integration

package repository

import (
	"context"
	"sync"
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

// ─── cms-users-api §10 新增方法 integration 測試 ───────────────────────────────

// TestCMSUserRepository_List_FilterByRole role 篩選只回對應 role
func TestCMSUserRepository_List_FilterByRole(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	for _, u := range []*model.CMSUser{
		{Base: model.Base{ID: uuid.New()}, Username: "i_admin", Role: "admin", PasswordHash: "h"},
		{Base: model.Base{ID: uuid.New()}, Username: "i_user", Role: "user", PasswordHash: "h"},
		{Base: model.Base{ID: uuid.New()}, Username: "i_viewer", Role: "viewer", PasswordHash: "h"},
	} {
		require.NoError(t, db.Create(u).Error)
	}

	users, total, err := repo.List(ctx, ListCMSUsersOptions{Page: 1, PageSize: 20, RoleFilter: []string{"admin", "user"}})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, users, 2)
}

// TestCMSUserRepository_List_UsernameLike_EscapesUnderscore `_` 必須被 escape，
// 不可當 SQL LIKE 萬用字元，否則 "a_b" 會誤配 "axb"
func TestCMSUserRepository_List_UsernameLike_EscapesUnderscore(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	for _, u := range []*model.CMSUser{
		{Base: model.Base{ID: uuid.New()}, Username: "a_b", Role: "user", PasswordHash: "h"},
		{Base: model.Base{ID: uuid.New()}, Username: "axb", Role: "user", PasswordHash: "h"},
	} {
		require.NoError(t, db.Create(u).Error)
	}

	users, total, err := repo.List(ctx, ListCMSUsersOptions{Page: 1, PageSize: 20, UsernameLike: "a_b"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	assert.Equal(t, "a_b", users[0].Username)
}

// TestCMSUserRepository_List_ExcludesSoftDeleted 預設不含軟刪除；include_deleted=true 才含
func TestCMSUserRepository_List_ExcludesSoftDeleted(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	active := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_active", Role: "user", PasswordHash: "h"}
	deleted := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_deleted", Role: "user", PasswordHash: "h"}
	require.NoError(t, db.Create(active).Error)
	require.NoError(t, db.Create(deleted).Error)
	require.NoError(t, db.Delete(deleted).Error)

	_, total, err := repo.List(ctx, ListCMSUsersOptions{Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	_, totalAll, err := repo.List(ctx, ListCMSUsersOptions{Page: 1, PageSize: 20, IncludeDeleted: true})
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalAll)
}

// TestCMSUserRepository_FindByID_SoftDeleted_NotFoundWithoutInclude
func TestCMSUserRepository_FindByID_SoftDeleted_NotFoundWithoutInclude(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	u := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_sd", Role: "user", PasswordHash: "h"}
	require.NoError(t, db.Create(u).Error)
	require.NoError(t, db.Delete(u).Error)

	_, err := repo.FindByID(ctx, u.ID.String(), false)
	assert.ErrorIs(t, err, apperr.ErrNotFound)

	found, err := repo.FindByID(ctx, u.ID.String(), true)
	require.NoError(t, err)
	assert.Equal(t, "i_sd", found.Username)
}

// TestCMSUserRepository_SoftDelete_Idempotency 第二次刪除 → ErrNotFound
func TestCMSUserRepository_SoftDelete_Idempotency(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	u := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_del", Role: "user", PasswordHash: "h"}
	require.NoError(t, db.Create(u).Error)

	require.NoError(t, repo.SoftDelete(ctx, u.ID.String()))
	err := repo.SoftDelete(ctx, u.ID.String())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestCMSUserRepository_Update_UsernameConflict_ReturnsErrConflict
func TestCMSUserRepository_Update_UsernameConflict_ReturnsErrConflict(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	taken := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_taken", Role: "user", PasswordHash: "h"}
	target := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_target", Role: "user", PasswordHash: "h"}
	require.NoError(t, db.Create(taken).Error)
	require.NoError(t, db.Create(target).Error)

	newName := "i_taken"
	err := repo.Update(ctx, target.ID.String(), CMSUserPatch{Username: &newName})
	assert.ErrorIs(t, err, apperr.ErrConflict)
}

// TestCMSUserRepository_CountActiveAdmins 只算未軟刪除的 admin
func TestCMSUserRepository_CountActiveAdmins(t *testing.T) {
	db := WithTx(t)
	repo := NewCMSUserRepository(db)
	ctx := context.Background()

	a1 := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_a1", Role: "admin", PasswordHash: "h"}
	a2 := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_a2", Role: "admin", PasswordHash: "h"}
	u1 := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "i_u1", Role: "user", PasswordHash: "h"}
	require.NoError(t, db.Create(a1).Error)
	require.NoError(t, db.Create(a2).Error)
	require.NoError(t, db.Create(u1).Error)
	require.NoError(t, db.Delete(a2).Error) // 軟刪一個 admin

	count, err := repo.CountActiveAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestCMSUserRepository_ConcurrentDemote_OnlyOneSucceeds 兩個並發降級僅有的兩個 admin，
// 透過 Transactor + CountActiveAdmins 的 admin-set FOR UPDATE 序列化，最多剩一個 admin（§12 #10）。
// 使用真實 commit（非 WithTx rollback），測試後手動硬刪除清理。
func TestCMSUserRepository_ConcurrentDemote_OnlyOneSucceeds(t *testing.T) {
	repo := NewCMSUserRepository(testDB)
	tx := NewTransactor(testDB)
	ctx := context.Background()

	a1 := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "race_a1_" + uuid.NewString(), Role: "admin", PasswordHash: "h"}
	a2 := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "race_a2_" + uuid.NewString(), Role: "admin", PasswordHash: "h"}
	require.NoError(t, testDB.Create(a1).Error)
	require.NoError(t, testDB.Create(a2).Error)
	t.Cleanup(func() {
		testDB.Unscoped().Delete(&model.CMSUser{}, "id IN ?", []uuid.UUID{a1.ID, a2.ID})
	})

	// demote 函式：模擬 service.Update 的 INV-1 transaction 邏輯
	demote := func(targetID string) error {
		return tx.WithTx(ctx, func(ctx context.Context) error {
			target, err := repo.FindByID(ctx, targetID, false)
			if err != nil {
				return err
			}
			if target.Role == "admin" {
				count, err := repo.CountActiveAdmins(ctx)
				if err != nil {
					return err
				}
				if count <= 1 {
					return apperr.ErrLastAdminLockout
				}
			}
			role := "viewer"
			return repo.Update(ctx, targetID, CMSUserPatch{Role: &role})
		})
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = demote(a1.ID.String()) }()
	go func() { defer wg.Done(); errs[1] = demote(a2.ID.String()) }()
	wg.Wait()

	// 至少有一個被 lockout 擋下（不可能兩個都成功，否則 0 admin）
	lockouts := 0
	for _, err := range errs {
		if err != nil {
			assert.ErrorIs(t, err, apperr.ErrLastAdminLockout)
			lockouts++
		}
	}
	assert.Equal(t, 1, lockouts, "恰一個 demote 應被 last_admin_lockout 擋下")

	count, err := repo.CountActiveAdmins(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, int64(1), "至少保留一個 admin")
}
