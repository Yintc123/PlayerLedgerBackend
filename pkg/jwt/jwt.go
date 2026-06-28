package jwt

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yintengching/playerledger/config"
)

// AccessClaims — access token claims 定义。
// RFC 7519 RegisteredClaims 提供 iss, sub, aud, exp, iat, jti。
// 额外字段：utype, role, fid。
type AccessClaims struct {
	jwt.RegisteredClaims
	UserType UserType `json:"utype"`
	Role     Role     `json:"role"`
	FamilyID string   `json:"fid"` // 对应后端 family，供管理员「廢掉 family 連帶 access」用
}

// RefreshClaims — refresh token claims 定义。
// RegisteredClaims 提供 iss, sub, aud, exp, iat, jti。
// 额外字段：utype, fid, abs_exp。
type RefreshClaims struct {
	jwt.RegisteredClaims
	UserType    UserType `json:"utype"`
	FamilyID    string   `json:"fid"`
	AbsoluteExp int64    `json:"abs_exp"` // unix seconds，rotation 不延长，超过则 ErrAbsoluteExpired
}

// UserID 是 RegisteredClaims.Subject 的具名 alias，遵循 JWT RFC 7519「sub claim 即 user identifier」慣例。
// 所有 middleware / service / audit 取 user ID 一律呼叫此 method，禁止直接讀 c.Subject。
func (c *AccessClaims) UserID() string  { return c.Subject }
func (c *RefreshClaims) UserID() string { return c.Subject }

// SignAccessParams — SignAccess 參數結構。
// Issuer 由 jwtManager 內部以 JWTConfig.Issuer 自動填入，不放 Params。
type SignAccessParams struct {
	UserID   string        // 寫入 JWT 的 sub（RegisteredClaims.Subject）
	UserType UserType      // user type（cms / member）
	Role     Role          // user role（admin / user / viewer / member）
	FamilyID string        // token 所屬 family（session）
	ClientID string        // 寫入 aud
	JTI      string        // 呼叫端產生（每次都新）
	TTL      time.Duration // 預設取 JWTConfig.AccessTTL
}

// SignRefreshParams — SignRefresh 參數結構。
type SignRefreshParams struct {
	UserID      string        // 寫入 sub
	UserType    UserType      // user type
	FamilyID    string        // token 所屬 family
	ClientID    string        // 寫入 aud
	JTI         string        // Login=新；Rotated=新；GraceHit=取 state.CurrentJTI
	TTL         time.Duration // 來自 client policy 的 RefreshTTL
	AbsoluteExp time.Time     // 必須從 server-side state 取（FamilyState.AbsoluteExp），rotation/grace 不改
}

// Manager 所有 method 第一參數均為 context.Context，對齊規範「所有 service / repository 簽章必接 ctx」原則。
// Sign / Verify 本身為 in-memory HMAC 計算，不會 cancel；ctx 主要保留給未來 OTel span 與測試
// （fake manager 可從 ctx 取注入值），降低介面演進成本。
type Manager interface {
	SignAccess(ctx context.Context, p SignAccessParams) (token string, err error)
	VerifyAccess(ctx context.Context, token string) (*AccessClaims, error)
	SignRefresh(ctx context.Context, p SignRefreshParams) (token string, err error)
	VerifyRefresh(ctx context.Context, token string) (*RefreshClaims, error)

	// PolicyOf 從 client_id 取對應 ClientPolicy；找不到回 ErrInvalidClient。
	PolicyOf(ctx context.Context, clientID string) (config.ClientPolicy, error)
}

type manager struct {
	cfg                config.JWTConfig
	clientIDWhitelist  map[string]bool
}

// NewManager 由 cfg 構造 Manager。
// 簽章：一律使用 cfg.Secret / cfg.RefreshSecret，演算法固定 HS256。
// 詳見規格 §8.3 NewManager 文件。
func NewManager(cfg config.JWTConfig) Manager {
	whitelist := make(map[string]bool)
	for clientID := range cfg.ClientPolicies {
		whitelist[clientID] = true
	}

	return &manager{
		cfg:               cfg,
		clientIDWhitelist: whitelist,
	}
}

// SignAccess 簽署 access token。
// Issuer 由此內部自動填入（JWTConfig.Issuer）。
func (m *manager) SignAccess(ctx context.Context, p SignAccessParams) (string, error) {
	now := time.Now()
	claims := &AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.UserID,
			Issuer:    m.cfg.Issuer,
			Audience:  jwt.ClaimStrings{p.ClientID},
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

// SignRefresh 簽署 refresh token。
// Issuer 由此內部自動填入。
func (m *manager) SignRefresh(ctx context.Context, p SignRefreshParams) (string, error) {
	now := time.Now()
	claims := &RefreshClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.UserID,
			Issuer:    m.cfg.Issuer,
			Audience:  jwt.ClaimStrings{p.ClientID},
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

// VerifyAccess 驗證 access token。
// 驗證流程（詳見規格 §8.3）：
//   0. Alg 鎖定 HS256（防 alg=none / alg confusion）
//   1. 簽章先試主 secret，失敗再試 PreviousSecret（若已設定）→ 都失敗回 ErrInvalidToken
//   2. iss 必須 == cfg.Issuer
//   3. aud 必須 ∈ 已知 client_id 白名單
//   4. 時間 claim 含 leeway
func (m *manager) VerifyAccess(ctx context.Context, tokenString string) (*AccessClaims, error) {
	keyFn := func(secret string) jwt.Keyfunc {
		return func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != "HS256" {
				return nil, fmt.Errorf("invalid algorithm: expected HS256, got %s", t.Method.Alg())
			}
			return []byte(secret), nil
		}
	}

	// 以 leeway 解析（§8.3 ClockSkewLeeway）
	parser := jwt.NewParser(jwt.WithLeeway(m.cfg.ClockSkewLeeway))

	token, err := parser.ParseWithClaims(tokenString, &AccessClaims{}, keyFn(m.cfg.Secret))

	// 若失敗且有 PreviousSecret，嘗試舊 secret（§8.3 secret rotation）
	if err != nil && m.cfg.PreviousSecret != "" {
		token, err = parser.ParseWithClaims(tokenString, &AccessClaims{}, keyFn(m.cfg.PreviousSecret))
	}

	if err != nil {
		// 區分「過期」與「其他錯誤」（§8.3）
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*AccessClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	// iss 檢查（§8.3 步驟 2）
	if claims.Issuer != m.cfg.Issuer {
		return nil, ErrInvalidToken
	}

	// aud 白名單檢查（§8.3 步驟 3）
	if len(claims.Audience) == 0 || !m.clientIDWhitelist[claims.Audience[0]] {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// VerifyRefresh 驗證 refresh token。
// 驗證流程（詳見規格 §8.3）：
//   0-3. 同 VerifyAccess（alg / secret / iss / aud）
//   4. 時間 claim 含 leeway
//   5. abs_exp 額外檢查：abs_exp + leeway > now，否則 ErrAbsoluteExpired
func (m *manager) VerifyRefresh(ctx context.Context, tokenString string) (*RefreshClaims, error) {
	keyFn := func(secret string) jwt.Keyfunc {
		return func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != "HS256" {
				return nil, fmt.Errorf("invalid algorithm: expected HS256, got %s", t.Method.Alg())
			}
			return []byte(secret), nil
		}
	}

	// 以 leeway 解析（§8.3 ClockSkewLeeway）
	parser := jwt.NewParser(jwt.WithLeeway(m.cfg.ClockSkewLeeway))

	token, err := parser.ParseWithClaims(tokenString, &RefreshClaims{}, keyFn(m.cfg.RefreshSecret))

	// 若失敗且有 PreviousRefreshSecret，嘗試舊 secret（§8.3 secret rotation）
	if err != nil && m.cfg.PreviousRefreshSecret != "" {
		token, err = parser.ParseWithClaims(tokenString, &RefreshClaims{}, keyFn(m.cfg.PreviousRefreshSecret))
	}

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	// iss 檢查（§8.3 步驟 2）
	if claims.Issuer != m.cfg.Issuer {
		return nil, ErrInvalidToken
	}

	// aud 白名單檢查（§8.3 步驟 3）
	if len(claims.Audience) == 0 || !m.clientIDWhitelist[claims.Audience[0]] {
		return nil, ErrInvalidToken
	}

	// 步驟 5：abs_exp 額外檢查（§8.3）— 含 leeway，abs_exp 由 server state 管控
	now := time.Now()
	absExpTime := time.Unix(claims.AbsoluteExp, 0)
	if now.After(absExpTime.Add(m.cfg.ClockSkewLeeway)) {
		return nil, ErrAbsoluteExpired
	}

	return claims, nil
}

// PolicyOf 從 client_id 取對應 ClientPolicy；找不到回 ErrInvalidClient。
func (m *manager) PolicyOf(ctx context.Context, clientID string) (config.ClientPolicy, error) {
	policy, ok := m.cfg.ClientPolicies[clientID]
	if !ok {
		return config.ClientPolicy{}, ErrInvalidClient
	}
	return policy, nil
}
