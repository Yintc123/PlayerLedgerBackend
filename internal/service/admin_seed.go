package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
)

// EnsureAdminFromConfig 啟動時依 AdminConfig idempotent 確保 super admin 帳號存在。
// 取代規格 §13.5 原 SQL seed migration——seed 改由應用層在啟動時完成，
// 避免把任何形式的密碼（含 placeholder）寫進版控的 migration SQL。
//
// 行為：
//   - username/password 皆空 → 跳過（dev 友善）。Validate() 已保證兩欄一致性。
//   - 帳號不存在 → bcrypt(password) → 建立。
//   - 帳號已存在 → log info「skipping」，**不主動覆寫密碼**（避免無聲改密碼造成稽核盲點；
//     旋密請走 CMS API 或 ops 手動 update）。
//
// 回傳 (created bool, err error)：created=true 表示本次有建立帳號。
func EnsureAdminFromConfig(
	ctx context.Context,
	repo repository.CMSUserRepository,
	h hasher.Hasher,
	username, password string,
) (bool, error) {
	if username == "" && password == "" {
		return false, nil
	}

	_, err := repo.FindByUsername(ctx, username)
	if err == nil {
		return false, nil // 已存在 → 跳過
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return false, fmt.Errorf("lookup admin: %w", err)
	}

	hashed, err := h.Hash(password)
	if err != nil {
		return false, fmt.Errorf("hash admin password: %w", err)
	}

	if err := repo.Create(ctx, &model.CMSUser{
		Username:     username,
		PasswordHash: hashed,
		Role:         "admin",
	}); err != nil {
		return false, fmt.Errorf("create admin: %w", err)
	}
	return true, nil
}
