package jwt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/apperr"
)

func TestNewManager(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	assert.NotNil(t, mgr)
}

func TestSignAccessToken(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      15 * time.Minute,
	}

	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// 验证令牌格式（3 部分）
	parts := strings.Split(token, ".")
	assert.Len(t, parts, 3)
}

func TestSignRefreshToken(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	absExp := time.Now().Add(30 * 24 * time.Hour)
	params := SignRefreshParams{
		UserID:      "user-123",
		UserType:    UserTypeCMS,
		FamilyID:    "fid-456",
		ClientID:    "cms-web",
		JTI:         "jti-refresh",
		TTL:         1 * time.Hour,
		AbsoluteExp: absExp,
	}

	token, err := mgr.SignRefresh(ctx, params)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestVerifyAccessToken_Valid(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 签署令牌
	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      15 * time.Minute,
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)

	// 验证令牌
	claims, err := mgr.VerifyAccess(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID())
	assert.Equal(t, UserTypeCMS, claims.UserType)
	assert.Equal(t, RoleAdmin, claims.Role)
	assert.Equal(t, "fid-456", claims.FamilyID)
	assert.Equal(t, "jti-789", claims.ID)
	assert.Equal(t, "cms-web", claims.Audience[0])
	assert.Equal(t, cfg.Issuer, claims.Issuer)
}

func TestVerifyAccessToken_InvalidToken(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	_, err := mgr.VerifyAccess(ctx, "invalid.token.string")
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyAccessToken_AlgNone_ShouldReject(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 创建手动的 alg=none token（模拟攻击）
	// 标准库不允许签署 alg=none，但我们可以创建一个令牌字符串来测试拒绝逻辑
	// 以下是 alg=none 令牌的格式示例（不带签名）
	noneToken := "eyJhbGciOiJub25lIn0.eyJzdWIiOiJhdHRhY2tlciIsImlzcyI6InBsYXllcmxlZGdlciIsImF1ZCI6WyJjbXMtd2ViIl19."

	// 应当拒绝
	_, err := mgr.VerifyAccess(ctx, noneToken)
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyAccessToken_AlgConfusion_ShouldReject(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 尝试用 RS256（不同的 alg）
	claims := &AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "attacker",
			Issuer:    cfg.Issuer,
			Audience:  jwt.ClaimStrings{"cms-web"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        "confused-jti",
		},
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fake-fid",
	}

	// 用 HS512 签署（不同的算法）
	// 为了测试目的，我们创建一个不同算法的令牌
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	tokenString, _ := token.SignedString([]byte(cfg.Secret))

	// 应当拒绝（不是 HS256）
	_, err := mgr.VerifyAccess(ctx, tokenString)
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyAccessToken_ExpiredToken(t *testing.T) {
	cfg := newTestConfig()
	cfg.ClockSkewLeeway = 0 // 严格模式，无容差
	mgr := NewManager(cfg)
	ctx := context.Background()

	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      -1 * time.Second, // 已过期
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)

	_, err = mgr.VerifyAccess(ctx, token)
	assert.True(t, errors.Is(err, apperr.ErrTokenExpired))
}

func TestVerifyAccessToken_ClockSkewLeeway(t *testing.T) {
	cfg := newTestConfig()
	cfg.ClockSkewLeeway = 60 * time.Second
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 创建刚好过期但在 leeway 范围内的令牌
	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      -10 * time.Second, // 10 秒前过期
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)

	// 应当通过（在 leeway 范围内）
	claims, err := mgr.VerifyAccess(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID())
}

func TestVerifyAccessToken_InvalidIssuer(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 签署有效令牌
	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      15 * time.Minute,
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)

	// 改变 issuer 配置
	wrongCfg := newTestConfig()
	wrongCfg.Issuer = "wrong-issuer"
	wrongMgr := NewManager(wrongCfg)

	// 应当拒绝
	_, err = wrongMgr.VerifyAccess(ctx, token)
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyAccessToken_InvalidAudience(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 签署有效令牌
	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      15 * time.Minute,
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)

	// 移除 cms-web 的 policy
	wrongCfg := newTestConfig()
	wrongCfg.ClientPolicies = map[string]config.ClientPolicy{
		"public-web": cfg.ClientPolicies["public-web"],
	}
	wrongMgr := NewManager(wrongCfg)

	// 应当拒绝（aud 不在白名单）
	_, err = wrongMgr.VerifyAccess(ctx, token)
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyAccessToken_PreviousSecretFallback(t *testing.T) {
	// 用旧 secret 签署
	oldCfg := config.JWTConfig{
		Issuer:          "playerledger",
		Secret:          "old-secret-must-be-at-least-32-chars-long-xxx",
		RefreshSecret:   "test-refresh-secret-must-be-32-chars-long-xxxxx",
		AccessTTL:       15 * time.Minute,
		ClockSkewLeeway: 30 * time.Second,
		ClientPolicies: map[string]config.ClientPolicy{
			"cms-web": {
				RefreshTTL:  1 * time.Hour,
				AbsoluteTTL: 8 * time.Hour,
			},
		},
	}
	oldMgr := NewManager(oldCfg)
	ctx := context.Background()

	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      15 * time.Minute,
	}
	token, err := oldMgr.SignAccess(ctx, params)
	require.NoError(t, err)

	// 用新配置验证（新 secret + 旧 secret 作为 fallback）
	newCfg := config.JWTConfig{
		Issuer:            "playerledger",
		Secret:            "new-secret-must-be-at-least-32-chars-long-xxx",
		PreviousSecret:    "old-secret-must-be-at-least-32-chars-long-xxx",
		RefreshSecret:     "test-refresh-secret-must-be-32-chars-long-xxxxx",
		AccessTTL:         15 * time.Minute,
		ClockSkewLeeway:   30 * time.Second,
		ClientPolicies:    oldCfg.ClientPolicies,
	}
	newMgr := NewManager(newCfg)
	claims, err := newMgr.VerifyAccess(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID())
}

func TestVerifyRefreshToken_Valid(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	absExp := time.Now().Add(30 * 24 * time.Hour)
	params := SignRefreshParams{
		UserID:      "user-123",
		UserType:    UserTypeCMS,
		FamilyID:    "fid-456",
		ClientID:    "cms-web",
		JTI:         "jti-refresh",
		TTL:         1 * time.Hour,
		AbsoluteExp: absExp,
	}
	token, err := mgr.SignRefresh(ctx, params)
	require.NoError(t, err)

	claims, err := mgr.VerifyRefresh(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID())
	assert.Equal(t, UserTypeCMS, claims.UserType)
	assert.Equal(t, "fid-456", claims.FamilyID)
	assert.Equal(t, "jti-refresh", claims.ID)
	assert.Equal(t, absExp.Unix(), claims.AbsoluteExp)
}

func TestVerifyRefreshToken_AbsoluteExpired(t *testing.T) {
	cfg := newTestConfig()
	cfg.ClockSkewLeeway = 0
	mgr := NewManager(cfg)
	ctx := context.Background()

	// abs_exp 已过期
	absExp := time.Now().Add(-1 * time.Hour)
	params := SignRefreshParams{
		UserID:      "user-123",
		UserType:    UserTypeCMS,
		FamilyID:    "fid-456",
		ClientID:    "cms-web",
		JTI:         "jti-refresh",
		TTL:         1 * time.Hour, // exp 未过期
		AbsoluteExp: absExp,        // 但 abs_exp 过期
	}
	token, err := mgr.SignRefresh(ctx, params)
	require.NoError(t, err)

	_, err = mgr.VerifyRefresh(ctx, token)
	assert.True(t, errors.Is(err, apperr.ErrAbsoluteExpired))
}

func TestVerifyRefreshToken_AbsoluteExpWithLeeway(t *testing.T) {
	cfg := newTestConfig()
	cfg.ClockSkewLeeway = 60 * time.Second
	mgr := NewManager(cfg)
	ctx := context.Background()

	// abs_exp 刚过期，但在 leeway 范围内
	absExp := time.Now().Add(-10 * time.Second)
	params := SignRefreshParams{
		UserID:      "user-123",
		UserType:    UserTypeCMS,
		FamilyID:    "fid-456",
		ClientID:    "cms-web",
		JTI:         "jti-refresh",
		TTL:         1 * time.Hour,
		AbsoluteExp: absExp,
	}
	token, err := mgr.SignRefresh(ctx, params)
	require.NoError(t, err)

	// 应当通过（在 leeway 范围内）
	claims, err := mgr.VerifyRefresh(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID())
}

func TestPolicyOf_ValidClient(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	policy, err := mgr.PolicyOf(ctx, "cms-web")
	require.NoError(t, err)
	assert.Equal(t, 1*time.Hour, policy.RefreshTTL)
	assert.Equal(t, 8*time.Hour, policy.AbsoluteTTL)
}

func TestPolicyOf_InvalidClient(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	_, err := mgr.PolicyOf(ctx, "unknown-client")
	assert.True(t, errors.Is(err, apperr.ErrInvalidClient))
}

func TestVerifyAccessToken_InvalidSecret(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	params := SignAccessParams{
		UserID:   "user-123",
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
		ClientID: "cms-web",
		JTI:      "jti-789",
		TTL:      15 * time.Minute,
	}
	token, err := mgr.SignAccess(ctx, params)
	require.NoError(t, err)

	// 用错误的 secret 验证
	wrongCfg := newTestConfig()
	wrongCfg.Secret = "wrong-secret-must-be-at-least-32-chars-long"
	wrongMgr := NewManager(wrongCfg)

	_, err = wrongMgr.VerifyAccess(ctx, token)
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyRefreshToken_InvalidSecret(t *testing.T) {
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	absExp := time.Now().Add(30 * 24 * time.Hour)
	params := SignRefreshParams{
		UserID:      "user-123",
		UserType:    UserTypeCMS,
		FamilyID:    "fid-456",
		ClientID:    "cms-web",
		JTI:         "jti-refresh",
		TTL:         1 * time.Hour,
		AbsoluteExp: absExp,
	}
	token, err := mgr.SignRefresh(ctx, params)
	require.NoError(t, err)

	// 用错误的 refresh secret 验证
	wrongCfg := newTestConfig()
	wrongCfg.RefreshSecret = "wrong-refresh-secret-must-be-32-chars"
	wrongMgr := NewManager(wrongCfg)

	_, err = wrongMgr.VerifyRefresh(ctx, token)
	assert.True(t, errors.Is(err, apperr.ErrInvalidToken))
}

func TestVerifyAccessToken_SigningMethodCheck(t *testing.T) {
	// 确保在 keyfunc 中检查 alg（早期拒绝）
	cfg := newTestConfig()
	mgr := NewManager(cfg)
	ctx := context.Background()

	// 创建 HS512 的令牌（不同算法）
	claims := &AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			Issuer:    cfg.Issuer,
			Audience:  jwt.ClaimStrings{"cms-web"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        "jti-789",
		},
		UserType: UserTypeCMS,
		Role:     RoleAdmin,
		FamilyID: "fid-456",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	tokenString, tokenErr := token.SignedString([]byte(cfg.Secret))
	require.NoError(t, tokenErr)

	// 应当拒绝（不是 HS256）
	_, verifyErr := mgr.VerifyAccess(ctx, tokenString)
	assert.True(t, errors.Is(verifyErr, apperr.ErrInvalidToken))
}

// Helper function
func newTestConfig() config.JWTConfig {
	return config.JWTConfig{
		Issuer:          "playerledger",
		Secret:          "test-secret-must-be-at-least-32-chars-long-xxxxx",
		RefreshSecret:   "test-refresh-secret-must-be-32-chars-long-xxxxx",
		AccessTTL:       15 * time.Minute,
		ClockSkewLeeway: 30 * time.Second,
		ClientPolicies: map[string]config.ClientPolicy{
			"cms-web": {
				RefreshTTL:  1 * time.Hour,
				AbsoluteTTL: 8 * time.Hour,
			},
			"public-web": {
				RefreshTTL:  1 * time.Hour,
				AbsoluteTTL: 24 * time.Hour,
			},
			"ios-app": {
				RefreshTTL:  720 * time.Hour, // 30d
				AbsoluteTTL: 4320 * time.Hour, // 180d
			},
		},
	}
}
