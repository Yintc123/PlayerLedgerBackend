package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/logger"
	"github.com/yintengching/playerledger/pkg/metrics"
	"github.com/yintengching/playerledger/pkg/redis"
	"go.uber.org/zap"
)

// CMSUserService CMS 內部人員管理業務介面（cms-users-api §9）。
type CMSUserService interface {
	List(ctx context.Context, opts repository.ListCMSUsersOptions) ([]model.CMSUser, int64, error)
	Get(ctx context.Context, id string, includeDeleted bool) (*model.CMSUser, error)
	// Update 改 username / role；role 變更時集中檢查 INV-1 / INV-3 並觸發強制 revoke（§4.3）。
	Update(ctx context.Context, callerID, targetID string, in UpdateCMSUserInput) (*model.CMSUser, error)
	// SoftDelete 軟刪除；檢查 INV-1 / INV-2 並觸發強制 revoke（§4.4）。
	SoftDelete(ctx context.Context, callerID, targetID string) error
	// UpdateSelf caller 改自己 username / password；不能改 role（INV-4）。
	UpdateSelf(ctx context.Context, callerID string, in UpdateSelfInput) (*model.CMSUser, error)
}

// UpdateCMSUserInput PATCH /cms/users/{id} 輸入；nil = 不改（§9）。
type UpdateCMSUserInput struct {
	Username *string
	Role     *string // 非 nil 時 service 檢 INV-1 / INV-3，並觸發 §4.3 強制 revoke
}

// UpdateSelfInput PATCH /cms/users/me 輸入；刻意不暴露 Role（INV-4，§9）。
type UpdateSelfInput struct {
	Username        *string
	CurrentPassword *string // 若 NewPassword 非 nil 則必填
	NewPassword     *string
}

type cmsUserService struct {
	repo           repository.CMSUserRepository
	tx             repository.Transactor
	hasher         hasher.Hasher
	familyStore    redis.FamilyStore
	userRevocation redis.UserRevocationStore
	revocationTTL  time.Duration // §4.3 公式算出：max(ClientPolicies.AbsoluteTTL) + leeway
	audit          audit.Logger
}

// NewCMSUserService 建立 CMS user 管理服務（§9）。
// userRevocationTTL 由 cmd/server/main.go 啟動時依 max(ClientPolicies.AbsoluteTTL)+24h 計算後注入。
func NewCMSUserService(
	repo repository.CMSUserRepository,
	tx repository.Transactor,
	h hasher.Hasher,
	familyStore redis.FamilyStore,
	userRevocation redis.UserRevocationStore,
	userRevocationTTL time.Duration,
	auditLogger audit.Logger,
) CMSUserService {
	return &cmsUserService{
		repo:           repo,
		tx:             tx,
		hasher:         h,
		familyStore:    familyStore,
		userRevocation: userRevocation,
		revocationTTL:  userRevocationTTL,
		audit:          auditLogger,
	}
}

func (s *cmsUserService) List(ctx context.Context, opts repository.ListCMSUsersOptions) ([]model.CMSUser, int64, error) {
	return s.repo.List(ctx, opts)
}

func (s *cmsUserService) Get(ctx context.Context, id string, includeDeleted bool) (*model.CMSUser, error) {
	return s.repo.FindByID(ctx, id, includeDeleted)
}

// Update 更新 username / role（§4.3）。INV 檢查與 UPDATE 同 transaction，
// redis revoke 與 audit 在 commit 後 best-effort 執行。
func (s *cmsUserService) Update(ctx context.Context, callerID, targetID string, in UpdateCMSUserInput) (*model.CMSUser, error) {
	var updated *model.CMSUser
	var fromRole string
	roleChanged := false

	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		target, err := s.repo.FindByID(ctx, targetID, false) // SELECT … FOR UPDATE
		if err != nil {
			return err
		}
		fromRole = target.Role

		if in.Role != nil {
			// INV-1：最後一個 admin 不可降級
			if target.Role == "admin" && *in.Role != "admin" {
				count, err := s.repo.CountActiveAdmins(ctx)
				if err != nil {
					return err
				}
				if count <= 1 {
					return apperr.ErrLastAdminLockout
				}
			}
			// INV-3：caller 不能改自己的 role
			if callerID == targetID {
				return apperr.ErrCannotChangeOwnRole
			}
			if *in.Role != target.Role {
				roleChanged = true
			}
		}

		patch := repository.CMSUserPatch{Username: in.Username, Role: in.Role}
		if err := s.repo.Update(ctx, targetID, patch); err != nil {
			if errors.Is(err, apperr.ErrConflict) {
				return apperr.ErrUsernameTaken
			}
			return err
		}

		updated, err = s.repo.FindByID(ctx, targetID, false)
		return err
	})
	if err != nil {
		return nil, err
	}

	// ── post-commit best-effort side effects（§4.3）──
	if roleChanged {
		s.forceRevoke(ctx, targetID)
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:         audit.EventCMSUserUpdated,
		UserID:       callerID,
		TargetUserID: targetID,
		Extra: map[string]any{
			"username_changed": in.Username != nil,
			"role_changed":     roleChanged,
		},
	})
	if roleChanged {
		s.audit.Log(ctx, audit.AuthEvent{
			Type:         audit.EventCMSUserRoleChanged,
			UserID:       callerID,
			TargetUserID: targetID,
			Extra:        map[string]any{"from": fromRole, "to": *in.Role},
		})
		s.audit.Log(ctx, audit.AuthEvent{
			Type:         audit.EventCMSUserSessionsForceRevoked,
			UserID:       callerID,
			TargetUserID: targetID,
			Extra:        map[string]any{"reason": "role_changed"},
		})
	}

	return updated, nil
}

// SoftDelete 軟刪除（§4.4）。
func (s *cmsUserService) SoftDelete(ctx context.Context, callerID, targetID string) error {
	// INV-2：caller 不能刪自己
	if callerID == targetID {
		return apperr.ErrCannotDeleteSelf
	}

	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		target, err := s.repo.FindByID(ctx, targetID, false) // SELECT … FOR UPDATE
		if err != nil {
			return err
		}
		// INV-1：最後一個 admin 不可刪
		if target.Role == "admin" {
			count, err := s.repo.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return apperr.ErrLastAdminLockout
			}
		}
		return s.repo.SoftDelete(ctx, targetID)
	})
	if err != nil {
		return err
	}

	// ── post-commit best-effort side effects（§4.4）──
	s.forceRevoke(ctx, targetID)

	s.audit.Log(ctx, audit.AuthEvent{
		Type:         audit.EventCMSUserDeleted,
		UserID:       callerID,
		TargetUserID: targetID,
	})
	s.audit.Log(ctx, audit.AuthEvent{
		Type:         audit.EventCMSUserSessionsForceRevoked,
		UserID:       callerID,
		TargetUserID: targetID,
		Extra:        map[string]any{"reason": "deleted"},
	})

	return nil
}

// UpdateSelf caller 改自己 username / password（§4.5）。改密碼不觸發 revoke。
func (s *cmsUserService) UpdateSelf(ctx context.Context, callerID string, in UpdateSelfInput) (*model.CMSUser, error) {
	patch := repository.CMSUserPatch{Username: in.Username}
	passwordChanged := false

	if in.NewPassword != nil {
		// 改密碼前必須驗舊密碼（§4.5）
		if in.CurrentPassword == nil {
			return nil, apperr.ErrInvalidInput
		}
		current, err := s.repo.FindByID(ctx, callerID, false)
		if err != nil {
			return nil, err
		}
		if err := s.hasher.Compare(current.PasswordHash, *in.CurrentPassword); err != nil {
			if errors.Is(err, hasher.ErrMismatch) {
				return nil, apperr.ErrCurrentPasswordMismatch
			}
			return nil, fmt.Errorf("compare current password: %w", err)
		}
		if isWeakPassword(*in.NewPassword) {
			return nil, apperr.ErrWeakPassword
		}
		hash, err := s.hasher.Hash(*in.NewPassword)
		if err != nil {
			return nil, fmt.Errorf("hash new password: %w", err)
		}
		patch.PasswordHash = &hash
		passwordChanged = true
	}

	// 深度防禦：至少改一個欄位（handler OpenAPI minProperties 已擋）
	if patch.Username == nil && patch.PasswordHash == nil {
		return nil, apperr.ErrInvalidInput
	}

	if err := s.repo.Update(ctx, callerID, patch); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			return nil, apperr.ErrUsernameTaken
		}
		return nil, err
	}

	updated, err := s.repo.FindByID(ctx, callerID, false)
	if err != nil {
		return nil, err
	}

	extra := map[string]any{}
	if passwordChanged {
		extra["password_changed"] = true
	}
	// TargetUserID 留空：self_updated 的 actor 即 target（§7）
	s.audit.Log(ctx, audit.AuthEvent{
		Type:   audit.EventCMSUserSelfUpdated,
		UserID: callerID,
		Extra:  extra,
	})

	return updated, nil
}

// forceRevoke 廢掉 target 所有 session（§4.3 / §4.4 post-commit）。
// 失敗僅 log warn + metric，不影響主操作回應（§7 audit fail policy）。
func (s *cmsUserService) forceRevoke(ctx context.Context, targetID string) {
	if err := s.familyStore.RevokeAll(ctx, targetID); err != nil {
		logger.L().Warn("force revoke: family RevokeAll failed",
			zap.String("target_user_id", targetID), zap.Error(err))
		metrics.AuthUserRevokeErrors.Inc()
	}
	if err := s.userRevocation.Revoke(ctx, targetID, s.revocationTTL); err != nil {
		logger.L().Warn("force revoke: user revocation watermark failed",
			zap.String("target_user_id", targetID), zap.Error(err))
		metrics.AuthUserRevokeErrors.Inc()
	}
}
