package service

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/redis"
)

// AuthService 定义认证服务接口。
type AuthService interface {
	Register(ctx context.Context, username, password, clientID string) error
	Login(ctx context.Context, username, password, clientID string) (*TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*TokenPair, error)
	Logout(ctx context.Context, userID, fid, accessJTI string) error
	ListSessions(ctx context.Context, userID string) ([]SessionInfo, error)
	RevokeSession(ctx context.Context, userID, currentFid, targetFid string) error
	RevokeAll(ctx context.Context, userID string) error
}

// TokenPair access + refresh token 对。
type TokenPair struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
}

// SessionInfo 会话信息（GET /auth/sessions 返回）。
type SessionInfo struct {
	FID            string    `json:"fid"`
	ClientID       string    `json:"client_id"`
	DeviceLabel    string    `json:"device_label"`
	IPAtLogin      string    `json:"ip_at_login"`
	CreatedAt      time.Time `json:"created_at"`
	LastRotatedAt  time.Time `json:"last_rotated_at"`
	IsCurrent      bool      `json:"is_current"`
}

type authService struct {
	cmsUserRepo  repository.CMSUserRepository
	memberRepo   repository.MemberRepository
	jwtManager   jwt.Manager
	hasher       hasher.Hasher
	familyStore  redis.FamilyStore
	blacklist    redis.AccessTokenBlacklist
}

// NewAuthService 创建认证服务。
func NewAuthService(
	cmsUserRepo repository.CMSUserRepository,
	memberRepo repository.MemberRepository,
	jwtManager jwt.Manager,
	h hasher.Hasher,
	familyStore redis.FamilyStore,
	blacklist redis.AccessTokenBlacklist,
) AuthService {
	return &authService{
		cmsUserRepo: cmsUserRepo,
		memberRepo:  memberRepo,
		jwtManager:  jwtManager,
		hasher:      h,
		familyStore: familyStore,
		blacklist:   blacklist,
	}
}

// isWeakPassword 检查密码强度：至少 8 字符，包含字母和数字。
func isWeakPassword(password string) bool {
	if len(password) < 8 {
		return true
	}
	hasLetter := regexp.MustCompile(`[a-zA-Z]`).MatchString(password)
	hasDigit := regexp.MustCompile(`[0-9]`).MatchString(password)
	return !(hasLetter && hasDigit)
}

// Register 注册新 CMS 用户（仅 cms-web）。
func (s *authService) Register(ctx context.Context, username, password, clientID string) error {
	if clientID != "cms-web" {
		return apperr.ErrInvalidClient
	}

	if isWeakPassword(password) {
		return apperr.ErrWeakPassword
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}

	user := &model.CMSUser{
		Username:     username,
		PasswordHash: hash,
		Role:         string(jwt.RoleUser),
	}

	if err := s.cmsUserRepo.Create(ctx, user); err != nil {
		return err
	}

	return nil
}

// Login 登入：验证帐密，签发 token pair，保存 family。
func (s *authService) Login(ctx context.Context, username, password, clientID string) (*TokenPair, error) {
	policy, err := s.jwtManager.PolicyOf(clientID)
	if err != nil {
		return nil, apperr.ErrInvalidClient
	}

	now := time.Now()
	absExp := now.Add(policy.AbsoluteTTL)
	fid := uuid.New().String()
	accessJTI := uuid.New().String()
	refreshJTI := uuid.New().String()

	// 路由：cms-web -> cms_users，其他 -> members
	var userID, userType string
	var userRole string

	if clientID == "cms-web" {
		user, err := s.cmsUserRepo.FindByUsername(ctx, username)
		if err != nil {
			return nil, apperr.ErrUnauthorized
		}

		ok, err := s.hasher.Compare(user.PasswordHash, password)
		if err != nil || !ok {
			return nil, apperr.ErrUnauthorized
		}

		userID = user.ID.String()
		userType = "cms"
		userRole = user.Role
	} else {
		member, err := s.memberRepo.FindByUsername(ctx, username)
		if err != nil {
			return nil, apperr.ErrUnauthorized
		}

		ok, err := s.hasher.Compare(member.PasswordHash, password)
		if err != nil || !ok {
			return nil, apperr.ErrUnauthorized
		}

		userID = member.ID.String()
		userType = "member"
		userRole = "member"
	}

	// 签发 tokens
	accessToken, err := s.jwtManager.SignAccess(jwt.SignAccessParams{
		UserID:   userID,
		UserType: jwt.UserType(userType),
		Role:     jwt.Role(userRole),
		FamilyID: fid,
		ClientID: clientID,
		JTI:      accessJTI,
		TTL:      policy.RefreshTTL, // 使用 RefreshTTL 作为 access TTL（应为 15m）
	})
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.jwtManager.SignRefresh(jwt.SignRefreshParams{
		UserID:      userID,
		UserType:    jwt.UserType(userType),
		FamilyID:    fid,
		ClientID:    clientID,
		JTI:         refreshJTI,
		TTL:         policy.RefreshTTL,
		AbsoluteExp: absExp,
	})
	if err != nil {
		return nil, err
	}

	// 保存 family state
	familyState := redis.FamilyState{
		UserID:        userID,
		FamilyID:      fid,
		ClientID:      clientID,
		UserType:      userType,
		Role:          userRole,
		CurrentJTI:    refreshJTI,
		AbsoluteExp:   absExp.Unix(),
		DeviceLabel:   "Unknown", // TODO: 从 User-Agent 解析
		IPAtLogin:     "0.0.0.0", // TODO: 从 request 取
		CreatedAt:     now.Unix(),
		LastRotatedAt: now.Unix(),
	}

	if err := s.familyStore.Save(ctx, familyState); err != nil {
		return nil, fmt.Errorf("save family: %w", err)
	}

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        int64(policy.RefreshTTL.Seconds()),
		RefreshExpiresIn: int64(policy.RefreshTTL.Seconds()),
	}, nil
}

// Refresh refresh token rotation（ADR-007）。
func (s *authService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.jwtManager.VerifyRefresh(refreshToken)
	if err != nil {
		return nil, err
	}

	userID := claims.UserID()
	fid := claims.FamilyID
	presentedJTI := claims.ID
	newJTI := uuid.New().String()

	policy, err := s.jwtManager.PolicyOf(claims.Audience[0])
	if err != nil {
		return nil, err
	}

	// 调用 FamilyStore.Rotate
	result, state, err := s.familyStore.Rotate(ctx, userID, fid, presentedJTI, newJTI, policy.RefreshTTL)
	if err != nil {
		return nil, err
	}

	if result == redis.FamilyNotFound {
		return nil, apperr.ErrSessionNotFound
	}

	if result == redis.ReplayDetected {
		// TODO: audit.Log(replay_detected)
		// TODO: metrics.AuthReplayDetected.Inc()
		return nil, apperr.ErrReplayDetected
	}

	if state == nil {
		return nil, apperr.ErrSessionNotFound
	}

	// GraceHit 或 Rotated：重签 tokens
	var useJTI string
	if result == redis.GraceHit {
		useJTI = state.CurrentJTI
	} else {
		useJTI = newJTI
	}

	accessToken, err := s.jwtManager.SignAccess(jwt.SignAccessParams{
		UserID:   userID,
		UserType: jwt.UserType(state.UserType),
		Role:     jwt.Role(state.Role),
		FamilyID: fid,
		ClientID: claims.Audience[0],
		JTI:      uuid.New().String(),
		TTL:      policy.RefreshTTL,
	})
	if err != nil {
		return nil, err
	}

	refreshTokenNew, err := s.jwtManager.SignRefresh(jwt.SignRefreshParams{
		UserID:      userID,
		UserType:    jwt.UserType(state.UserType),
		FamilyID:    fid,
		ClientID:    claims.Audience[0],
		JTI:         useJTI,
		TTL:         policy.RefreshTTL,
		AbsoluteExp: time.Unix(state.AbsoluteExp, 0),
	})
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshTokenNew,
		TokenType:        "Bearer",
		ExpiresIn:        int64(policy.RefreshTTL.Seconds()),
		RefreshExpiresIn: int64(policy.RefreshTTL.Seconds()),
	}, nil
}

// Logout 登出：廢 family + 黑名单 access JTI。
func (s *authService) Logout(ctx context.Context, userID, fid, accessJTI string) error {
	if err := s.familyStore.Revoke(ctx, userID, fid); err != nil {
		return err
	}

	// TODO: 计算 TTL = access token 剩余 exp
	// TODO: s.blacklist.Add(ctx, accessJTI, ttl)

	return nil
}

// ListSessions 列出所有 session。
func (s *authService) ListSessions(ctx context.Context, userID string) ([]SessionInfo, error) {
	states, err := s.familyStore.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	var sessions []SessionInfo
	for _, state := range states {
		sessions = append(sessions, SessionInfo{
			FID:           state.FamilyID,
			ClientID:      state.ClientID,
			DeviceLabel:   state.DeviceLabel,
			IPAtLogin:     state.IPAtLogin,
			CreatedAt:     time.Unix(state.CreatedAt, 0),
			LastRotatedAt: time.Unix(state.LastRotatedAt, 0),
			IsCurrent:     false, // TODO: 从 current access claims 比对
		})
	}

	return sessions, nil
}

// RevokeSession 撤销指定 session（防守性检查不能撤自己当前 family）。
func (s *authService) RevokeSession(ctx context.Context, userID, currentFid, targetFid string) error {
	if currentFid == targetFid {
		return apperr.New("forbidden", nil, "use logout instead")
	}

	return s.familyStore.Revoke(ctx, userID, targetFid)
}

// RevokeAll 全装置登出。
func (s *authService) RevokeAll(ctx context.Context, userID string) error {
	return s.familyStore.RevokeAll(ctx, userID)
}
