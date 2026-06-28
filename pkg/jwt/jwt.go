package jwt

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/apperr"
)

// AccessClaims access token 的 claims。
type AccessClaims struct {
	jwt.RegisteredClaims
	UserType UserType `json:"utype"`
	Role     Role     `json:"role"`
	FamilyID string   `json:"fid"`
}

// RefreshClaims refresh token 的 claims。
type RefreshClaims struct {
	jwt.RegisteredClaims
	UserType    UserType `json:"utype"`
	FamilyID    string   `json:"fid"`
	AbsoluteExp int64    `json:"abs_exp"`
}

// UserID 返回 subject（user ID）。
func (c *AccessClaims) UserID() string  { return c.Subject }
func (c *RefreshClaims) UserID() string { return c.Subject }

// SignAccessParams access token 签署参数。
type SignAccessParams struct {
	UserID   string
	UserType UserType
	Role     Role
	FamilyID string
	ClientID string
	JTI      string
	TTL      time.Duration
}

// SignRefreshParams refresh token 签署参数。
type SignRefreshParams struct {
	UserID      string
	UserType    UserType
	FamilyID    string
	ClientID    string
	JTI         string
	TTL         time.Duration
	AbsoluteExp time.Time
}

// Manager JWT 签发与验证接口。
type Manager interface {
	SignAccess(p SignAccessParams) (string, error)
	VerifyAccess(token string) (*AccessClaims, error)
	SignRefresh(p SignRefreshParams) (string, error)
	VerifyRefresh(token string) (*RefreshClaims, error)
	PolicyOf(clientID string) (config.ClientPolicy, error)
}

type manager struct {
	cfg              config.JWTConfig
	clientIDWhitelist map[string]bool
}

// NewManager 创建 JWT Manager。
func NewManager(cfg config.JWTConfig) Manager {
	whitelist := make(map[string]bool)
	for clientID := range cfg.ClientPolicies {
		whitelist[clientID] = true
	}

	return &manager{
		cfg:              cfg,
		clientIDWhitelist: whitelist,
	}
}

// SignAccess 签署 access token。
func (m *manager) SignAccess(p SignAccessParams) (string, error) {
	now := time.Now()
	claims := &AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.UserID,
			Issuer:    m.cfg.Issuer,
			Audience:  []string{p.ClientID},
			ExpiresAt: jwt.NewNumericDate(now.Add(p.TTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        p.JTI,
		},
		UserType: p.UserType,
		Role:     p.Role,
		FamilyID: p.FamilyID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.cfg.Secret))
}

// SignRefresh 签署 refresh token。
func (m *manager) SignRefresh(p SignRefreshParams) (string, error) {
	now := time.Now()
	claims := &RefreshClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.UserID,
			Issuer:    m.cfg.Issuer,
			Audience:  []string{p.ClientID},
			ExpiresAt: jwt.NewNumericDate(now.Add(p.TTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        p.JTI,
		},
		UserType:    p.UserType,
		FamilyID:    p.FamilyID,
		AbsoluteExp: p.AbsoluteExp.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.cfg.RefreshSecret))
}

// VerifyAccess 验证 access token。
func (m *manager) VerifyAccess(tokenString string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Alg 锁定 HS256
		if token.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("invalid algorithm: %s", token.Method.Alg())
		}

		// 先试主 secret
		return []byte(m.cfg.Secret), nil
	})

	if err != nil {
		return nil, apperr.ErrInvalidToken
	}

	claims, ok := token.Claims.(*AccessClaims)
	if !ok || !token.Valid {
		return nil, apperr.ErrInvalidToken
	}

	// Issuer 检查
	if claims.Issuer != m.cfg.Issuer {
		return nil, apperr.ErrInvalidToken
	}

	// Audience 检查
	if len(claims.Audience) == 0 || !m.clientIDWhitelist[claims.Audience[0]] {
		return nil, apperr.ErrInvalidToken
	}

	// 含 leeway 的时间检查（由 jwt 库自动处理）
	now := time.Now().Unix()
	if claims.ExpiresAt != nil && now > claims.ExpiresAt.Time.Unix() + int64(m.cfg.ClockSkewLeeway.Seconds()) {
		return nil, apperr.ErrTokenExpired
	}

	return claims, nil
}

// VerifyRefresh 验证 refresh token。
func (m *manager) VerifyRefresh(tokenString string) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &RefreshClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Alg 锁定 HS256
		if token.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("invalid algorithm: %s", token.Method.Alg())
		}

		return []byte(m.cfg.RefreshSecret), nil
	})

	if err != nil {
		return nil, apperr.ErrInvalidToken
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, apperr.ErrInvalidToken
	}

	// Issuer 检查
	if claims.Issuer != m.cfg.Issuer {
		return nil, apperr.ErrInvalidToken
	}

	// Audience 检查
	if len(claims.Audience) == 0 || !m.clientIDWhitelist[claims.Audience[0]] {
		return nil, apperr.ErrInvalidToken
	}

	// exp 检查
	now := time.Now().Unix()
	if claims.ExpiresAt != nil && now > claims.ExpiresAt.Time.Unix() + int64(m.cfg.ClockSkewLeeway.Seconds()) {
		return nil, apperr.ErrTokenExpired
	}

	// abs_exp 检查
	if now > claims.AbsoluteExp + int64(m.cfg.ClockSkewLeeway.Seconds()) {
		return nil, apperr.ErrAbsoluteExpired
	}

	return claims, nil
}

// PolicyOf 获取 client 的 policy。
func (m *manager) PolicyOf(clientID string) (config.ClientPolicy, error) {
	policy, ok := m.cfg.ClientPolicies[clientID]
	if !ok {
		return config.ClientPolicy{}, apperr.ErrInvalidClient
	}
	return policy, nil
}
