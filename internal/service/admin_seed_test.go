package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureAdminFromConfig_BothEmpty_Skipped(t *testing.T) {
	repo := newFakeCMSUserRepository()
	h := &fakeHasher{}

	created, err := EnsureAdminFromConfig(context.Background(), repo, h, "", "")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Empty(t, repo.users)
}

func TestEnsureAdminFromConfig_NotExists_Created(t *testing.T) {
	repo := newFakeCMSUserRepository()
	h := &fakeHasher{}

	created, err := EnsureAdminFromConfig(context.Background(), repo, h, "root", "long-enough-pw-123")
	require.NoError(t, err)
	assert.True(t, created)
	got, _ := repo.FindByUsername(context.Background(), "root")
	require.NotNil(t, got)
	assert.Equal(t, "root", got.Username)
	assert.Equal(t, "admin", got.Role)
	assert.Equal(t, "long-enough-pw-123", got.PasswordHash) // fakeHasher echoes plaintext
}

func TestEnsureAdminFromConfig_AlreadyExists_NotOverwritten(t *testing.T) {
	repo := newFakeCMSUserRepository()
	h := &fakeHasher{}

	// 先插入一個 admin（密碼 X）
	_, err := EnsureAdminFromConfig(context.Background(), repo, h, "root", "original-pw-abc")
	require.NoError(t, err)

	// 再用不同密碼呼叫 — 應跳過，不覆寫
	created, err := EnsureAdminFromConfig(context.Background(), repo, h, "root", "new-different-pw")
	require.NoError(t, err)
	assert.False(t, created, "已存在的 admin 不應被建立")

	got, _ := repo.FindByUsername(context.Background(), "root")
	assert.Equal(t, "original-pw-abc", got.PasswordHash, "已存在的 admin 密碼不應被覆寫")
}
