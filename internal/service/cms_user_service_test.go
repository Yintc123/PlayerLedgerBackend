package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/redis"
)

// ─── fakes（CMS service 專用）─────────────────────────────────────────────────

// fakeTransactor 直接執行 fn（測試不需真 DB transaction）。
type fakeTransactor struct{}

func (t *fakeTransactor) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// spyFamilyStore 記錄 RevokeAll 呼叫對象，其餘方法 no-op。
type spyFamilyStore struct {
	revokeAllCalls []string
}

func (s *spyFamilyStore) Save(context.Context, redis.FamilyState) error { return nil }
func (s *spyFamilyStore) Rotate(context.Context, string, string, string, string, time.Duration) (redis.RotateResult, *redis.FamilyState, error) {
	return redis.FamilyNotFound, nil, nil
}
func (s *spyFamilyStore) Revoke(context.Context, string, string) (bool, error) { return true, nil }
func (s *spyFamilyStore) RevokeAll(_ context.Context, userID string) error {
	s.revokeAllCalls = append(s.revokeAllCalls, userID)
	return nil
}
func (s *spyFamilyStore) ListByUser(context.Context, string) ([]redis.FamilyState, error) {
	return nil, nil
}
func (s *spyFamilyStore) ScriptsLoaded() bool { return true }

// spyUserRevocation 記錄 Revoke 呼叫對象。
type spyUserRevocation struct {
	revokeCalls []string
}

func (s *spyUserRevocation) Revoke(_ context.Context, userID string, _ time.Duration) error {
	s.revokeCalls = append(s.revokeCalls, userID)
	return nil
}
func (s *spyUserRevocation) RevokedAfter(context.Context, string) (int64, error) { return 0, nil }

// ─── helpers ─────────────────────────────────────────────────────────────────

func newCMSUserSvc(repo repository.CMSUserRepository, fam redis.FamilyStore, rev redis.UserRevocationStore, a audit.Logger) CMSUserService {
	return NewCMSUserService(repo, &fakeTransactor{}, &fakeHasher{}, fam, rev, time.Hour, a)
}

func seedCMSUser(t *testing.T, repo repository.CMSUserRepository, username, role, passwordHash string) *model.CMSUser {
	t.Helper()
	u := &model.CMSUser{Username: username, Role: role, PasswordHash: passwordHash}
	require.NoError(t, repo.Create(context.Background(), u))
	return u
}

func strptr(s string) *string { return &s }

// ─── Update / INV ──────────────────────────────────────────────────────────────

// TestCMSUserService_Update_LastAdminLockout 唯一 admin 降級 → ErrLastAdminLockout（INV-1）
func TestCMSUserService_Update_LastAdminLockout(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	admin := seedCMSUser(t, repo, "onlyadmin", "admin", "h")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.Update(context.Background(), "caller", admin.ID.String(), UpdateCMSUserInput{Role: strptr("viewer")})
	assert.ErrorIs(t, err, apperr.ErrLastAdminLockout)
}

// TestCMSUserService_Update_RoleChange_TriggersRevoke role 變動 → RevokeAll + Revoke 被呼叫（§4.3）
func TestCMSUserService_Update_RoleChange_TriggersRevoke(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	seedCMSUser(t, repo, "admin1", "admin", "h")
	target := seedCMSUser(t, repo, "admin2", "admin", "h")
	fam := &spyFamilyStore{}
	rev := &spyUserRevocation{}
	a := &captureAudit{}
	svc := newCMSUserSvc(repo, fam, rev, a)

	updated, err := svc.Update(context.Background(), "caller", target.ID.String(), UpdateCMSUserInput{Role: strptr("viewer")})
	require.NoError(t, err)
	assert.Equal(t, "viewer", updated.Role)
	assert.Equal(t, []string{target.ID.String()}, fam.revokeAllCalls)
	assert.Equal(t, []string{target.ID.String()}, rev.revokeCalls)

	// audit：updated + role_changed + sessions_force_revoked
	types := auditTypes(a)
	assert.Contains(t, types, audit.EventCMSUserUpdated)
	assert.Contains(t, types, audit.EventCMSUserRoleChanged)
	assert.Contains(t, types, audit.EventCMSUserSessionsForceRevoked)
}

// TestCMSUserService_Update_UsernameOnly_NoRevoke 只改 username（role 未動）不觸發 revoke（§12.3）
func TestCMSUserService_Update_UsernameOnly_NoRevoke(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	target := seedCMSUser(t, repo, "bob", "user", "h")
	fam := &spyFamilyStore{}
	rev := &spyUserRevocation{}
	a := &captureAudit{}
	svc := newCMSUserSvc(repo, fam, rev, a)

	updated, err := svc.Update(context.Background(), "caller", target.ID.String(), UpdateCMSUserInput{Username: strptr("bob2")})
	require.NoError(t, err)
	assert.Equal(t, "bob2", updated.Username)
	assert.Empty(t, fam.revokeAllCalls)
	assert.Empty(t, rev.revokeCalls)
	assert.NotContains(t, auditTypes(a), audit.EventCMSUserRoleChanged)
}

// TestCMSUserService_Update_OwnRole_ReturnsCannotChangeOwnRole caller 改自己 role → INV-3
func TestCMSUserService_Update_OwnRole_ReturnsCannotChangeOwnRole(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	seedCMSUser(t, repo, "admin1", "admin", "h")
	caller := seedCMSUser(t, repo, "admin2", "admin", "h")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.Update(context.Background(), caller.ID.String(), caller.ID.String(), UpdateCMSUserInput{Role: strptr("viewer")})
	assert.ErrorIs(t, err, apperr.ErrCannotChangeOwnRole)
}

// TestCMSUserService_Update_UsernameConflict username 衝突 → ErrUsernameTaken
func TestCMSUserService_Update_UsernameConflict(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	seedCMSUser(t, repo, "taken", "user", "h")
	target := seedCMSUser(t, repo, "bob", "user", "h")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.Update(context.Background(), "caller", target.ID.String(), UpdateCMSUserInput{Username: strptr("taken")})
	assert.ErrorIs(t, err, apperr.ErrUsernameTaken)
}

// TestCMSUserService_Update_NotFound 目標不存在 → ErrNotFound
func TestCMSUserService_Update_NotFound(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.Update(context.Background(), "caller", "11111111-1111-1111-1111-111111111111", UpdateCMSUserInput{Username: strptr("x")})
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// ─── SoftDelete ─────────────────────────────────────────────────────────────────

// TestCMSUserService_SoftDelete_CannotDeleteSelf caller 刪自己 → INV-2
func TestCMSUserService_SoftDelete_CannotDeleteSelf(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	err := svc.SoftDelete(context.Background(), "same-id", "same-id")
	assert.ErrorIs(t, err, apperr.ErrCannotDeleteSelf)
}

// TestCMSUserService_SoftDelete_LastAdmin 刪唯一 admin → ErrLastAdminLockout
func TestCMSUserService_SoftDelete_LastAdmin(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	admin := seedCMSUser(t, repo, "onlyadmin", "admin", "h")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	err := svc.SoftDelete(context.Background(), "caller", admin.ID.String())
	assert.ErrorIs(t, err, apperr.ErrLastAdminLockout)
}

// TestCMSUserService_SoftDelete_TriggersRevoke 軟刪後觸發 revoke（§4.4）
func TestCMSUserService_SoftDelete_TriggersRevoke(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	seedCMSUser(t, repo, "admin1", "admin", "h")
	target := seedCMSUser(t, repo, "victim", "user", "h")
	fam := &spyFamilyStore{}
	rev := &spyUserRevocation{}
	a := &captureAudit{}
	svc := newCMSUserSvc(repo, fam, rev, a)

	err := svc.SoftDelete(context.Background(), "caller", target.ID.String())
	require.NoError(t, err)
	assert.Equal(t, []string{target.ID.String()}, fam.revokeAllCalls)
	assert.Equal(t, []string{target.ID.String()}, rev.revokeCalls)

	types := auditTypes(a)
	assert.Contains(t, types, audit.EventCMSUserDeleted)
	assert.Contains(t, types, audit.EventCMSUserSessionsForceRevoked)

	// 軟刪後再查（不含 deleted）→ 視為不存在
	_, err = svc.Get(context.Background(), target.ID.String(), false)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestCMSUserService_SoftDelete_Idempotency 重複刪除 → 第二次 ErrNotFound（§4.4）
func TestCMSUserService_SoftDelete_Idempotency(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	seedCMSUser(t, repo, "admin1", "admin", "h")
	target := seedCMSUser(t, repo, "victim", "user", "h")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	require.NoError(t, svc.SoftDelete(context.Background(), "caller", target.ID.String()))
	err := svc.SoftDelete(context.Background(), "caller", target.ID.String())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// ─── UpdateSelf ─────────────────────────────────────────────────────────────────

// TestCMSUserService_UpdateSelf_NewPasswordRequiresCurrent 提供 new_password 缺 current → 400
func TestCMSUserService_UpdateSelf_NewPasswordRequiresCurrent(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	caller := seedCMSUser(t, repo, "alice", "user", "oldpw")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.UpdateSelf(context.Background(), caller.ID.String(), UpdateSelfInput{NewPassword: strptr("newpw123")})
	assert.ErrorIs(t, err, apperr.ErrInvalidInput)
}

// TestCMSUserService_UpdateSelf_UsernameNoCurrentPassword 只改 username 不需 current_password（§4.5）
func TestCMSUserService_UpdateSelf_UsernameNoCurrentPassword(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	caller := seedCMSUser(t, repo, "alice", "user", "oldpw")
	a := &captureAudit{}
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, a)

	updated, err := svc.UpdateSelf(context.Background(), caller.ID.String(), UpdateSelfInput{Username: strptr("alice2")})
	require.NoError(t, err)
	assert.Equal(t, "alice2", updated.Username)
	assert.Contains(t, auditTypes(a), audit.EventCMSUserSelfUpdated)
}

// TestCMSUserService_UpdateSelf_WrongCurrentPassword current_password 錯 → 401
func TestCMSUserService_UpdateSelf_WrongCurrentPassword(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	caller := seedCMSUser(t, repo, "alice", "user", "oldpw")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.UpdateSelf(context.Background(), caller.ID.String(), UpdateSelfInput{
		CurrentPassword: strptr("wrong"),
		NewPassword:     strptr("newpw123"),
	})
	assert.ErrorIs(t, err, apperr.ErrCurrentPasswordMismatch)
}

// TestCMSUserService_UpdateSelf_WeakNewPassword 弱新密碼 → 422
func TestCMSUserService_UpdateSelf_WeakNewPassword(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	caller := seedCMSUser(t, repo, "alice", "user", "oldpw")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.UpdateSelf(context.Background(), caller.ID.String(), UpdateSelfInput{
		CurrentPassword: strptr("oldpw"),
		NewPassword:     strptr("weak"), // < 8 字元
	})
	assert.ErrorIs(t, err, apperr.ErrWeakPassword)
}

// TestCMSUserService_UpdateSelf_PasswordChange_Success 改密碼成功，audit 標記 password_changed
func TestCMSUserService_UpdateSelf_PasswordChange_Success(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	caller := seedCMSUser(t, repo, "alice", "user", "oldpw")
	a := &captureAudit{}
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, a)

	_, err := svc.UpdateSelf(context.Background(), caller.ID.String(), UpdateSelfInput{
		CurrentPassword: strptr("oldpw"),
		NewPassword:     strptr("newpw123"),
	})
	require.NoError(t, err)
	require.Len(t, a.events, 1)
	assert.Equal(t, audit.EventCMSUserSelfUpdated, a.events[0].Type)
	assert.Equal(t, true, a.events[0].Extra["password_changed"])
}

// ─── Get / List ─────────────────────────────────────────────────────────────────

// TestCMSUserService_Get_NotFound 不存在 → ErrNotFound
func TestCMSUserService_Get_NotFound(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	_, err := svc.Get(context.Background(), "22222222-2222-2222-2222-222222222222", false)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestCMSUserService_List_FiltersByRole role 篩選只回對應 role
func TestCMSUserService_List_FiltersByRole(t *testing.T) {
	repo := repository.NewFakeCMSUserRepository()
	seedCMSUser(t, repo, "a", "admin", "h")
	seedCMSUser(t, repo, "u", "user", "h")
	seedCMSUser(t, repo, "v", "viewer", "h")
	svc := newCMSUserSvc(repo, &spyFamilyStore{}, &spyUserRevocation{}, audit.NewNopLogger())

	users, total, err := svc.List(context.Background(), repository.ListCMSUsersOptions{
		Page: 1, PageSize: 20, RoleFilter: []string{"admin"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	assert.Equal(t, "admin", users[0].Role)
}

func auditTypes(a *captureAudit) []audit.EventType {
	types := make([]audit.EventType, len(a.events))
	for i, e := range a.events {
		types[i] = e.Type
	}
	return types
}
