package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/redis"
	"golang.org/x/crypto/bcrypt"
)

// TestIsWeakPassword_TooShort 短于 8 字符
func TestIsWeakPassword_TooShort(t *testing.T) {
	assert.True(t, isWeakPassword("pass1"))
	assert.True(t, isWeakPassword(""))
}

// TestIsWeakPassword_NoLetter 无字母
func TestIsWeakPassword_NoLetter(t *testing.T) {
	assert.True(t, isWeakPassword("12345678"))
}

// TestIsWeakPassword_NoDigit 无数字
func TestIsWeakPassword_NoDigit(t *testing.T) {
	assert.True(t, isWeakPassword("password"))
}

// TestIsWeakPassword_Valid 有效密码（至少 8 字符，含字母和数字）
func TestIsWeakPassword_Valid(t *testing.T) {
	assert.False(t, isWeakPassword("password1"))
	assert.False(t, isWeakPassword("Pass1234")) // 至少 8 字符
	assert.False(t, isWeakPassword("admin123"))
}

// 假 repository 与工具函数用于单元測試

type fakeCMSUserRepository struct {
	users map[string]*model.CMSUser
}

func newFakeCMSUserRepository() *fakeCMSUserRepository {
	return &fakeCMSUserRepository{users: make(map[string]*model.CMSUser)}
}

func (r *fakeCMSUserRepository) FindByUsername(ctx context.Context, username string) (*model.CMSUser, error) {
	user, ok := r.users[username]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return user, nil
}

func (r *fakeCMSUserRepository) Create(ctx context.Context, u *model.CMSUser) error {
	if _, exists := r.users[u.Username]; exists {
		return apperr.ErrConflict
	}
	u.ID = uuid.New()
	r.users[u.Username] = u
	return nil
}

type fakeMemberRepository struct {
	members map[string]*model.Member
}

func newFakeMemberRepository() *fakeMemberRepository {
	return &fakeMemberRepository{members: make(map[string]*model.Member)}
}

func (r *fakeMemberRepository) FindByUsername(ctx context.Context, username string) (*model.Member, error) {
	member, ok := r.members[username]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return member, nil
}

type fakeHasher struct{}

func (h *fakeHasher) Hash(plain string) (string, error) {
	// 简单實作：直接返回密码（仅用于測試，生产禁止）
	return plain, nil
}

func (h *fakeHasher) Compare(hash, plain string) error {
	if hash != plain {
		return hasher.ErrMismatch
	}
	return nil
}

type fakeBcryptHasher struct {
	cost int
}

func newFakeBcryptHasher() *fakeBcryptHasher {
	return &fakeBcryptHasher{cost: 10}
}

func (h *fakeBcryptHasher) Hash(plain string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	return string(bytes), err
}

func (h *fakeBcryptHasher) Compare(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return hasher.ErrMismatch
	}
	return err
}

type fakeJWTManager struct {
	policies map[string]config.ClientPolicy
}

func newFakeJWTManager() *fakeJWTManager {
	return &fakeJWTManager{
		policies: map[string]config.ClientPolicy{
			"cms-web": {
				RefreshTTL:  time.Hour,
				AbsoluteTTL: 8 * time.Hour,
			},
			"public-web": {
				RefreshTTL:  time.Hour,
				AbsoluteTTL: 24 * time.Hour,
			},
		},
	}
}

func (m *fakeJWTManager) SignAccess(ctx context.Context, p jwt.SignAccessParams) (string, error) {
	return "fake_access_token_" + p.JTI, nil
}

func (m *fakeJWTManager) VerifyAccess(ctx context.Context, token string) (*jwt.AccessClaims, error) {
	return nil, nil
}

func (m *fakeJWTManager) SignRefresh(ctx context.Context, p jwt.SignRefreshParams) (string, error) {
	return "fake_refresh_token_" + p.JTI, nil
}

func (m *fakeJWTManager) VerifyRefresh(ctx context.Context, token string) (*jwt.RefreshClaims, error) {
	// 解析假 token 格式：fake_refresh_token_<jti>
	claims := &jwt.RefreshClaims{}
	claims.Subject = "fake_user_id"
	claims.ID = "fake_jti"
	claims.Audience = []string{"cms-web"}
	claims.FamilyID = "fake_fid"
	claims.AbsoluteExp = time.Now().Add(8 * time.Hour).Unix()
	return claims, nil
}

func (m *fakeJWTManager) PolicyOf(ctx context.Context, clientID string) (config.ClientPolicy, error) {
	policy, ok := m.policies[clientID]
	if !ok {
		return config.ClientPolicy{}, apperr.ErrInvalidClient
	}
	return policy, nil
}

type fakeFamilyStore struct {
	families        map[string]*redis.FamilyState
	lastGraceWindow time.Duration // 記錄最後一次 Rotate 收到的 graceWindow（regression: H1 grace window bug）
}

func newFakeFamilyStore() *fakeFamilyStore {
	return &fakeFamilyStore{families: make(map[string]*redis.FamilyState)}
}

func (s *fakeFamilyStore) Save(ctx context.Context, state redis.FamilyState) error {
	key := state.UserID + ":" + state.FamilyID
	s.families[key] = &state
	return nil
}

func (s *fakeFamilyStore) Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string, graceWindow time.Duration) (redis.RotateResult, *redis.FamilyState, error) {
	s.lastGraceWindow = graceWindow
	key := userID + ":" + fid
	state, ok := s.families[key]
	if !ok {
		return redis.FamilyNotFound, nil, nil
	}

	if state.CurrentJTI == presentedJTI {
		state.CurrentJTI = newJTI
		state.PreviousJTI = presentedJTI
		state.LastRotatedAt = time.Now().Unix()
		return redis.Rotated, state, nil
	}

	if state.PreviousJTI == presentedJTI && time.Now().Unix() < state.PreviousResponseUntil {
		return redis.GraceHit, state, nil
	}

	delete(s.families, key)
	return redis.ReplayDetected, nil, nil
}

func (s *fakeFamilyStore) Revoke(ctx context.Context, userID, fid string) error {
	key := userID + ":" + fid
	delete(s.families, key)
	return nil
}

func (s *fakeFamilyStore) RevokeAll(ctx context.Context, userID string) error {
	for key := range s.families {
		if len(key) > len(userID) && key[:len(userID)] == userID {
			delete(s.families, key)
		}
	}
	return nil
}

func (s *fakeFamilyStore) ListByUser(ctx context.Context, userID string) ([]redis.FamilyState, error) {
	var result []redis.FamilyState
	for key, state := range s.families {
		if len(key) > len(userID) && key[:len(userID)] == userID {
			result = append(result, *state)
		}
	}
	return result, nil
}

func (s *fakeFamilyStore) ScriptsLoaded() bool {
	return true
}

type fakeAccessTokenBlacklist struct {
	blacklist map[string]time.Time
}

func newFakeAccessTokenBlacklist() *fakeAccessTokenBlacklist {
	return &fakeAccessTokenBlacklist{blacklist: make(map[string]time.Time)}
}

func (b *fakeAccessTokenBlacklist) Add(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl > 0 {
		b.blacklist[jti] = time.Now().Add(ttl)
	}
	return nil
}

func (b *fakeAccessTokenBlacklist) IsBlacklisted(ctx context.Context, jti string) (bool, error) {
	exp, ok := b.blacklist[jti]
	if !ok {
		return false, nil
	}
	return time.Now().Before(exp), nil
}

// TestAuthService_Register_Success 注册成功（§8.9）
func TestAuthService_Register_Success(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := newFakeBcryptHasher()
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	in := RegisterInput{
		Username: "testuser",
		Password: "password123",
		ClientID: "cms-web",
	}

	err := svc.Register(ctx, in)
	assert.NoError(t, err)

	// 驗證用户已建立
	user, err := cmsUserRepo.FindByUsername(ctx, "testuser")
	assert.NoError(t, err)
	assert.Equal(t, "testuser", user.Username)
	assert.Equal(t, string(jwt.RoleUser), user.Role)
}

// TestAuthService_Register_WeakPassword 弱密码拒绝（§8.9）
func TestAuthService_Register_WeakPassword(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{}
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	tests := []struct {
		name     string
		password string
	}{
		{"too short", "pass1"},
		{"no digit", "password"},
		{"no letter", "12345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := RegisterInput{
				Username: "testuser_" + tt.name,
				Password: tt.password,
				ClientID: "cms-web",
			}

			err := svc.Register(ctx, in)
			assert.Equal(t, apperr.ErrWeakPassword, err)
		})
	}
}

// TestAuthService_Register_InvalidClient 非 cms-web 拒绝（§8.9）
func TestAuthService_Register_InvalidClient(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{}
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	in := RegisterInput{
		Username: "testuser",
		Password: "password123",
		ClientID: "public-web", // 只有 cms-web 允许
	}

	err := svc.Register(ctx, in)
	assert.Equal(t, apperr.ErrInvalidClient, err)
}

// TestAuthService_Register_UsernameTaken username 已占用（§8.9）
func TestAuthService_Register_UsernameTaken(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := newFakeBcryptHasher()
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	// 先注册一个用户
	in1 := RegisterInput{
		Username: "testuser",
		Password: "password123",
		ClientID: "cms-web",
	}
	_ = svc.Register(ctx, in1)

	// 再尝试注册同名用户
	in2 := RegisterInput{
		Username: "testuser",
		Password: "password456",
		ClientID: "cms-web",
	}
	err := svc.Register(ctx, in2)
	assert.Equal(t, apperr.ErrUsernameTaken, err)
}

// TestAuthService_Login_Success 登入成功（§8.9）
func TestAuthService_Login_Success(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := newFakeBcryptHasher()
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	// 先注册用户
	hash, _ := h.Hash("password123")
	user := &model.CMSUser{
		Username:     "testuser",
		PasswordHash: hash,
		Role:         "user",
	}
	user.ID = uuid.New()
	cmsUserRepo.users["testuser"] = user

	// 登入
	in := LoginInput{
		Username:  "testuser",
		Password:  "password123",
		ClientID:  "cms-web",
		IP:        "192.168.1.1",
		UserAgent: "Mozilla/5.0",
	}

	pair, err := svc.Login(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "Bearer", pair.TokenType)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, 900, pair.ExpiresIn) // access TTL = 15m = 900s（§8.2 固定 AccessTTL）
	assert.Equal(t, 3600, pair.RefreshExpiresIn)

	// 驗證 family 已保存
	states, _ := familyStore.ListByUser(ctx, user.ID.String())
	assert.Equal(t, 1, len(states))
}

// TestAuthService_Login_InvalidCredentials 登入失败（錯誤密码）
func TestAuthService_Login_InvalidCredentials(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{} // 使用 fakeHasher，不需要真正的 bcrypt
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	// 先注册用户
	hash, _ := h.Hash("password123")
	user := &model.CMSUser{
		Username:     "testuser",
		PasswordHash: hash,
		Role:         "user",
	}
	user.ID = uuid.New()
	cmsUserRepo.users["testuser"] = user

	// 尝试用錯誤密码登入
	in := LoginInput{
		Username:  "testuser",
		Password:  "wrongpassword",
		ClientID:  "cms-web",
		IP:        "192.168.1.1",
		UserAgent: "Mozilla/5.0",
	}

	_, err := svc.Login(ctx, in)
	assert.Equal(t, apperr.ErrUnauthorized, err)
}

// TestAuthService_ListSessions_WithCurrent 列出 session，标记当前（§8.9）
func TestAuthService_ListSessions_WithCurrent(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{}
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	userID := uuid.New().String()
	currentFID := uuid.New().String()
	otherFID := uuid.New().String()

	// 保存两个 family
	_ = familyStore.Save(ctx, redis.FamilyState{
		UserID:        userID,
		FamilyID:      currentFID,
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "user",
		CurrentJTI:    "jti1",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Chrome on macOS",
		IPAtLogin:     "192.168.1.1",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	})

	_ = familyStore.Save(ctx, redis.FamilyState{
		UserID:        userID,
		FamilyID:      otherFID,
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "user",
		CurrentJTI:    "jti2",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Safari on iOS",
		IPAtLogin:     "192.168.1.2",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	})

	sessions, err := svc.ListSessions(ctx, userID, currentFID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(sessions))

	// 驗證当前 session 被标记
	found := false
	for _, s := range sessions {
		if s.FID == currentFID {
			assert.True(t, s.IsCurrent)
			found = true
		} else {
			assert.False(t, s.IsCurrent)
		}
	}
	assert.True(t, found)
}

// TestAuthService_RevokeSession_CannotRevokeCurrent 不能撤销当前 session（§8.9）
func TestAuthService_RevokeSession_CannotRevokeCurrent(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{}
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	userID := uuid.New().String()
	currentFID := uuid.New().String()

	// 尝试撤销当前 session
	err := svc.RevokeSession(ctx, userID, currentFID, currentFID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// TestAuthService_Refresh_PassesGraceWindowToFamilyStore — regression test for H1。
//
// 早期 v1.9 前的 bug：Refresh 把 policy.RefreshTTL（小時/天）誤傳給 FamilyStore.Rotate
// 的 graceWindow 參數，導致被盜 refresh token 在數小時/天內反覆觸發 GraceHit 而不被
// 偵測為 replay，完全破壞 §8.2.1 重放偵測模型。
//
// 此測試固定參數對齊（10s grace 對 1h RefreshTTL）以確保不會再退化。
func TestAuthService_Refresh_PassesGraceWindowToFamilyStore(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{}
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	const graceWindow = 10 * time.Second
	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist,
		audit.NewNopLogger(), 15*time.Minute, graceWindow)

	// 預先建立一個 family，CurrentJTI 對齊 fakeJWTManager.VerifyRefresh 回傳的固定 jti
	userID := "fake_user_id"
	fid := "fake_fid"
	_ = familyStore.Save(ctx, redis.FamilyState{
		UserID:        userID,
		FamilyID:      fid,
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "user",
		CurrentJTI:    "fake_jti",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Chrome on macOS",
		IPAtLogin:     "192.168.1.1",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	})

	_, err := svc.Refresh(ctx, RefreshInput{
		RefreshToken: "fake_refresh_token_fake_jti",
		IP:           "192.168.1.1",
		UserAgent:    "Mozilla/5.0",
	})
	require.NoError(t, err)

	// H1 核心斷言：Rotate 收到的 graceWindow 必須是 cfg.JWT.GraceWindow，不是 policy.RefreshTTL
	assert.Equal(t, graceWindow, familyStore.lastGraceWindow,
		"Rotate 第 6 參數必須是 graceWindow（10s），不是 policy.RefreshTTL（1h）")
	assert.NotEqual(t, time.Hour, familyStore.lastGraceWindow,
		"若收到 1h，代表 H1 bug 復發：grace window 被誤傳為 RefreshTTL")
}

// TestAuthService_RevokeAll_BlacklistAccessJTI 全装置登出须加入黑名单（§8.9）
func TestAuthService_RevokeAll_BlacklistAccessJTI(t *testing.T) {
	ctx := context.Background()
	cmsUserRepo := newFakeCMSUserRepository()
	memberRepo := newFakeMemberRepository()
	h := &fakeHasher{}
	jwtMgr := newFakeJWTManager()
	familyStore := newFakeFamilyStore()
	blacklist := newFakeAccessTokenBlacklist()

	svc := NewAuthService(cmsUserRepo, memberRepo, jwtMgr, h, familyStore, blacklist, audit.NewNopLogger(), 15*time.Minute, 10*time.Second)

	userID := uuid.New().String()
	accessJTI := uuid.New().String()
	fid := uuid.New().String()

	// 保存一个 family
	_ = familyStore.Save(ctx, redis.FamilyState{
		UserID:        userID,
		FamilyID:      fid,
		ClientID:      "cms-web",
		UserType:      "cms",
		Role:          "user",
		CurrentJTI:    "jti1",
		AbsoluteExp:   time.Now().Add(8 * time.Hour).Unix(),
		DeviceLabel:   "Chrome on macOS",
		IPAtLogin:     "192.168.1.1",
		CreatedAt:     time.Now().Unix(),
		LastRotatedAt: time.Now().Unix(),
	})

	// 全装置登出
	ttl := 15 * time.Minute
	err := svc.RevokeAll(ctx, userID, accessJTI, ttl)
	require.NoError(t, err)

	// 驗證 family 已刪除
	states, _ := familyStore.ListByUser(ctx, userID)
	assert.Equal(t, 0, len(states))

	// 驗證 access JTI 已加入黑名单
	isBlacklisted, _ := blacklist.IsBlacklisted(ctx, accessJTI)
	assert.True(t, isBlacklisted)
}
