//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yintengching/playerledger/internal/model"
)

// TestTransactor_Commit_OnNilErr 成功路徑：fn 回 nil → commit；DB 看得到寫入
func TestTransactor_Commit_OnNilErr(t *testing.T) {
	tx := NewTransactor(testDB)
	repo := NewCMSUserRepository(testDB)
	ctx := context.Background()

	u := &model.CMSUser{
		Username:     "tx-commit-" + t.Name(),
		PasswordHash: "h",
		Role:         "user",
	}

	err := tx.WithTx(ctx, func(ctx context.Context) error {
		return repo.Create(ctx, u)
	})
	require.NoError(t, err)
	defer cleanupCMSUser(t, u.Username)

	got, err := repo.FindByUsername(ctx, u.Username)
	require.NoError(t, err, "after commit the row must be visible outside the tx")
	assert.Equal(t, u.Username, got.Username)
}

// TestTransactor_Rollback_OnNonNilErr 失敗路徑：fn 回 err → rollback；DB 看不到寫入
func TestTransactor_Rollback_OnNonNilErr(t *testing.T) {
	tx := NewTransactor(testDB)
	repo := NewCMSUserRepository(testDB)
	ctx := context.Background()

	sentinel := errors.New("user said rollback")
	username := "tx-rollback-" + t.Name()
	defer cleanupCMSUser(t, username)

	err := tx.WithTx(ctx, func(ctx context.Context) error {
		if err := repo.Create(ctx, &model.CMSUser{
			Username:     username,
			PasswordHash: "h",
			Role:         "user",
		}); err != nil {
			return err
		}
		return sentinel
	})

	require.ErrorIs(t, err, sentinel, "WithTx must return the inner fn error verbatim")

	_, err = repo.FindByUsername(ctx, username)
	require.Error(t, err, "rollback should hide the inserted row")
}

// TestTransactor_Rollback_OnPanic panic 路徑：fn panic → rollback + 重新 panic
func TestTransactor_Rollback_OnPanic(t *testing.T) {
	tx := NewTransactor(testDB)
	repo := NewCMSUserRepository(testDB)
	ctx := context.Background()

	username := "tx-panic-" + t.Name()
	defer cleanupCMSUser(t, username)

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic to propagate; got none")
		}
		_, err := repo.FindByUsername(ctx, username)
		assert.Error(t, err, "rollback after panic must hide the inserted row")
	}()

	_ = tx.WithTx(ctx, func(ctx context.Context) error {
		if err := repo.Create(ctx, &model.CMSUser{
			Username:     username,
			PasswordHash: "h",
			Role:         "user",
		}); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		panic("forced panic inside tx")
	})
}

// TestTransactor_NestedReusesOuter 巢狀 WithTx 應重用外層 tx，不開新 savepoint（簡單實作以 fn 嵌套）
func TestTransactor_NestedReusesOuter(t *testing.T) {
	tx := NewTransactor(testDB)
	repo := NewCMSUserRepository(testDB)
	ctx := context.Background()

	username := "tx-nested-" + t.Name()
	defer cleanupCMSUser(t, username)

	err := tx.WithTx(ctx, func(ctx context.Context) error {
		return tx.WithTx(ctx, func(ctx context.Context) error {
			return repo.Create(ctx, &model.CMSUser{
				Username:     username,
				PasswordHash: "h",
				Role:         "user",
			})
		})
	})
	require.NoError(t, err)

	got, err := repo.FindByUsername(ctx, username)
	require.NoError(t, err)
	assert.Equal(t, username, got.Username)
}

// TestDBFromCtx_OutsideTx_FallsBackToDefault 在沒有 tx 的 ctx 下 dbFromCtx 應回到傳入的預設 db
func TestDBFromCtx_OutsideTx_FallsBackToDefault(t *testing.T) {
	got := dbFromCtx(context.Background(), testDB)
	assert.Same(t, testDB, got, "outside any tx, dbFromCtx must return the fallback db pointer")
}

// TestDBFromCtx_InsideTx_ReturnsTxDB 在 tx 內 dbFromCtx 應回 tx 的 *gorm.DB（與 fallback 不同）
func TestDBFromCtx_InsideTx_ReturnsTxDB(t *testing.T) {
	tx := NewTransactor(testDB)
	ctx := context.Background()

	err := tx.WithTx(ctx, func(ctx context.Context) error {
		got := dbFromCtx(ctx, testDB)
		require.NotNil(t, got)
		assert.NotSame(t, testDB, got, "inside tx, dbFromCtx must return the tx-scoped *gorm.DB (≠ root db)")
		return nil
	})
	require.NoError(t, err)
}

// cleanupCMSUser 確保測試後 cms_users 中沒有殘留 row（包含軟刪除）
func cleanupCMSUser(t *testing.T, username string) {
	t.Helper()
	t.Cleanup(func() {
		testDB.Unscoped().Where("username = ?", username).Delete(&model.CMSUser{})
	})
}

// 確保 gorm 在編譯時被引用（避免 linter 抱怨未使用）
var _ = gorm.ErrRecordNotFound
