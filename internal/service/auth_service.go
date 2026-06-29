package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
	"github.com/yintengching/playerledger/pkg/metrics"
	"github.com/yintengching/playerledger/pkg/redis"
	"github.com/yintengching/playerledger/pkg/ua"
	"go.uber.org/zap"
)

// AuthService 定義認證服務介面（§8.9）。
type AuthService interface {
	Register(ctx context.Context, in RegisterInput) error
	Login(ctx context.Context, in LoginInput) (*TokenPair, error)
	Refresh(ctx context.Context, in RefreshInput) (*TokenPair, error)
	Logout(ctx context.Context, in LogoutInput) error
	ListSessions(ctx context.Context, userID, currentFID string) ([]SessionInfo, error)
	RevokeSession(ctx context.Context, userID, currentFID, targetFID string) error
	RevokeAll(ctx context.Context, userID, currentAccessJTI string, currentAccessRemaining time.Duration) error
}

// RegisterInput 註冊輸入（§8.9）。
type RegisterInput struct {
	Username string
	Password string
	ClientID string
}

// LoginInput 登入輸入（§8.9）。
type LoginInput struct {
	Username  string
	Password  string
	ClientID  string
	IP        string
	UserAgent string
}

// RefreshInput Refresh 輸入（§8.2）。
type RefreshInput struct {
	RefreshToken string
	IP           string
	UserAgent    string
}

// LogoutInput 登出輸入（§8.9）。
type LogoutInput struct {
	UserID       string
	FamilyID     string
	AccessJTI    string
	AccessRemain time.Duration
	RefreshToken string // optional；非空時驗 fid 與 access claims 一致（§8.2）
}

// TokenPair access + refresh token 對（§3.5.1 TokenPair schema）。
type TokenPair struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`         // access TTL 秒（§3.5.1）
	RefreshExpiresIn int    `json:"refresh_expires_in"` // refresh TTL 秒
}

// SessionInfo 會話資訊（GET /auth/sessions 回傳，§3.5.1 SessionInfo schema）。
type SessionInfo struct {
	FID           string    `json:"fid"`
	ClientID      string    `json:"client_id"`
	DeviceLabel   string    `json:"device_label"`
	IPAtLogin     string    `json:"ip_at_login"`
	CreatedAt     time.Time `json:"created_at"`
	LastRotatedAt time.Time `json:"last_rotated_at"`
	IsCurrent     bool      `json:"is_current"`
}

type authService struct {
	cmsUserRepo repository.CMSUserRepository
	memberRepo  repository.MemberRepository
	jwtManager  jwt.Manager
	hasher      hasher.Hasher
	familyStore redis.FamilyStore
	blacklist   redis.AccessTokenBlacklist
	audit       audit.Logger
	accessTTL   time.Duration // JWT_ACCESS_TTL，固定 access token 有效期（§8.2）
	graceWindow time.Duration // JWT_GRACE_WINDOW；Refresh rotation 重送容忍窗（§8.2.1）
}

// NewAuthService 建立認證服務（§8.9）。
// accessTTL 由 cfg.JWT.AccessTTL 傳入，確保 access token 永遠使用固定 TTL（§8.2）。
// graceWindow 由 cfg.JWT.GraceWindow 傳入；用於 FamilyStore.Rotate 重放偵測窗口（§8.2.1）。
func NewAuthService(
	cmsUserRepo repository.CMSUserRepository,
	memberRepo repository.MemberRepository,
	jwtManager jwt.Manager,
	h hasher.Hasher,
	familyStore redis.FamilyStore,
	blacklist redis.AccessTokenBlacklist,
	auditLogger audit.Logger,
	accessTTL time.Duration,
	graceWindow time.Duration,
) AuthService {
	return &authService{
		cmsUserRepo: cmsUserRepo,
		memberRepo:  memberRepo,
		jwtManager:  jwtManager,
		hasher:      h,
		familyStore: familyStore,
		blacklist:   blacklist,
		audit:       auditLogger,
		accessTTL:   accessTTL,
		graceWindow: graceWindow,
	}
}

// isWeakPassword 密碼強度檢查（§8.9）：≥8 字符且同時含字母與數字。
func isWeakPassword(password string) bool {
	if len(password) < 8 {
		return true
	}
	hasLetter := regexp.MustCompile(`[a-zA-Z]`).MatchString(password)
	hasDigit := regexp.MustCompile(`[0-9]`).MatchString(password)
	return !hasLetter || !hasDigit
}

// Register 建立 CMS user，預設 role = "user"（§8.9）。
// 僅 cms-web 放行；弱密碼 → ErrWeakPassword；username 已占 → ErrUsernameTaken。
// 不簽 token；caller 另打 /auth/login。
func (s *authService) Register(ctx context.Context, in RegisterInput) error {
	if in.ClientID != "cms-web" {
		return apperr.ErrInvalidClient
	}

	if isWeakPassword(in.Password) {
		s.audit.Log(ctx, audit.AuthEvent{
			Type:     audit.EventRegisterFailed,
			ClientID: in.ClientID,
			Extra:    map[string]any{"reason": "weak_password", "username": in.Username},
		})
		return apperr.ErrWeakPassword
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		logger.L().Error("hash password failed", zap.Error(err))
		return fmt.Errorf("hash password: %w", err)
	}

	user := &model.CMSUser{
		Username:     in.Username,
		PasswordHash: hash,
		Role:         string(jwt.RoleUser),
	}

	if err := s.cmsUserRepo.Create(ctx, user); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			s.audit.Log(ctx, audit.AuthEvent{
				Type:     audit.EventRegisterFailed,
				ClientID: in.ClientID,
				Extra:    map[string]any{"reason": "username_taken", "username": in.Username},
			})
			return apperr.ErrUsernameTaken // ← 回傳 ErrUsernameTaken 而非 ErrConflict（§8.9 / §12.4）
		}
		return fmt.Errorf("create cms user: %w", err)
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:     audit.EventRegisterSuccess,
		UserID:   user.ID.String(),
		ClientID: in.ClientID,
		Extra:    map[string]any{"username": in.Username, "role": "user"},
	})

	return nil
}

// transitJWTError 將 pkg/jwt 的 sentinel error 轉譯為 internal/apperr 的 domain error。
// 對應 §2.1 依賴方向：pkg/jwt 不能 import internal/apperr，由 service 層做轉譯
// （與 §8.3.2 hasher.ErrMismatch → apperr.ErrUnauthorized 同個 pattern）。
func transitJWTError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, jwt.ErrTokenExpired):
		return apperr.ErrTokenExpired
	case errors.Is(err, jwt.ErrAbsoluteExpired):
		return apperr.ErrAbsoluteExpired
	case errors.Is(err, jwt.ErrInvalidToken):
		return apperr.ErrInvalidToken
	case errors.Is(err, jwt.ErrInvalidClient):
		return apperr.ErrInvalidClient
	default:
		return err
	}
}

// Login 登入：依 client_id 路由，驗帳密 → 開新 family → 簽 token pair（§8.9）。
func (s *authService) Login(ctx context.Context, in LoginInput) (*TokenPair, error) {
	policy, err := s.jwtManager.PolicyOf(ctx, in.ClientID)
	if err != nil {
		return nil, transitJWTError(err)
	}

	now := time.Now()
	absExp := now.Add(policy.AbsoluteTTL)
	fid := uuid.New().String()
	accessJTI := uuid.New().String()
	refreshJTI := uuid.New().String()

	var userID, userType, userRole string

	if in.ClientID == "cms-web" {
		user, err := s.cmsUserRepo.FindByUsername(ctx, in.Username)
		if err != nil {
			s.audit.Log(ctx, audit.AuthEvent{
				Type:     audit.EventLoginFailed,
				ClientID: in.ClientID,
				IP:       in.IP,
				Extra:    map[string]any{"reason": "invalid_credentials"},
			})
			return nil, apperr.ErrUnauthorized
		}

		if err := s.hasher.Compare(user.PasswordHash, in.Password); err != nil {
			s.audit.Log(ctx, audit.AuthEvent{
				Type:     audit.EventLoginFailed,
				UserID:   user.ID.String(),
				ClientID: in.ClientID,
				IP:       in.IP,
				Extra:    map[string]any{"reason": "invalid_credentials"},
			})
			if errors.Is(err, hasher.ErrMismatch) {
				return nil, apperr.ErrUnauthorized
			}
			logger.L().Error("password compare failed", zap.Error(err))
			return nil, fmt.Errorf("compare password: %w", err)
		}

		userID = user.ID.String()
		userType = "cms"
		userRole = user.Role
	} else {
		member, err := s.memberRepo.FindByUsername(ctx, in.Username)
		if err != nil {
			s.audit.Log(ctx, audit.AuthEvent{
				Type:     audit.EventLoginFailed,
				ClientID: in.ClientID,
				IP:       in.IP,
				Extra:    map[string]any{"reason": "invalid_credentials"},
			})
			return nil, apperr.ErrUnauthorized
		}

		if err := s.hasher.Compare(member.PasswordHash, in.Password); err != nil {
			s.audit.Log(ctx, audit.AuthEvent{
				Type:     audit.EventLoginFailed,
				UserID:   member.ID.String(),
				ClientID: in.ClientID,
				IP:       in.IP,
				Extra:    map[string]any{"reason": "invalid_credentials"},
			})
			if errors.Is(err, hasher.ErrMismatch) {
				return nil, apperr.ErrUnauthorized
			}
			logger.L().Error("password compare failed", zap.Error(err))
			return nil, fmt.Errorf("compare password: %w", err)
		}

		userID = member.ID.String()
		userType = "member"
		userRole = "member"
	}

	// 簽發 access token — TTL 固定用 s.accessTTL（§8.2，不依 client policy）
	accessToken, err := s.jwtManager.SignAccess(ctx, jwt.SignAccessParams{
		UserID:   userID,
		UserType: jwt.UserType(userType),
		Role:     jwt.Role(userRole),
		FamilyID: fid,
		ClientID: in.ClientID,
		JTI:      accessJTI,
		TTL:      s.accessTTL,
	})
	if err != nil {
		logger.L().Error("sign access token failed", zap.Error(err))
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// 簽發 refresh token
	refreshToken, err := s.jwtManager.SignRefresh(ctx, jwt.SignRefreshParams{
		UserID:      userID,
		UserType:    jwt.UserType(userType),
		FamilyID:    fid,
		ClientID:    in.ClientID,
		JTI:         refreshJTI,
		TTL:         policy.RefreshTTL,
		AbsoluteExp: absExp,
	})
	if err != nil {
		logger.L().Error("sign refresh token failed", zap.Error(err))
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	deviceLabel := ua.ParseDeviceLabel(in.UserAgent)
	familyState := redis.FamilyState{
		UserID:        userID,
		FamilyID:      fid,
		ClientID:      in.ClientID,
		UserType:      userType,
		Role:          userRole,
		CurrentJTI:    refreshJTI,
		AbsoluteExp:   absExp.Unix(),
		DeviceLabel:   deviceLabel,
		IPAtLogin:     in.IP,
		CreatedAt:     now.Unix(),
		LastRotatedAt: now.Unix(),
	}

	if err := s.familyStore.Save(ctx, familyState); err != nil {
		logger.L().Error("save family failed", zap.Error(err))
		return nil, fmt.Errorf("save family: %w", err)
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:      audit.EventLoginSuccess,
		UserID:    userID,
		FamilyID:  fid,
		ClientID:  in.ClientID,
		IP:        in.IP,
		UserAgent: in.UserAgent,
	})

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(s.accessTTL.Seconds()),       // access TTL 秒（§3.5.1 expires_in）
		RefreshExpiresIn: int(policy.RefreshTTL.Seconds()), // refresh TTL 秒
	}, nil
}

// Refresh token rotation（§8.2 / §8.3.1）。
func (s *authService) Refresh(ctx context.Context, in RefreshInput) (*TokenPair, error) {
	claims, err := s.jwtManager.VerifyRefresh(ctx, in.RefreshToken)
	if err != nil {
		return nil, transitJWTError(err)
	}

	userID := claims.UserID()
	fid := claims.FamilyID
	clientID := claims.Audience[0]
	presentedJTI := claims.ID
	newJTI := uuid.New().String()

	policy, err := s.jwtManager.PolicyOf(ctx, clientID)
	if err != nil {
		return nil, transitJWTError(err)
	}

	// 第 6 參數是 grace window（重送容忍窗，秒級），**不是** RefreshTTL（小時/天）；
	// 傳錯會導致被盜 token 在數小時/天內反覆觸發 GraceHit 而不被偵測為 replay，
	// 完全破壞 §8.2.1 重放偵測模型（v1.9 前的安全 bug，已修）。
	result, state, err := s.familyStore.Rotate(ctx, userID, fid, presentedJTI, newJTI, s.graceWindow)
	if err != nil {
		logger.L().Error("family rotate failed", zap.Error(err))
		return nil, fmt.Errorf("rotate family: %w", err)
	}

	if result == redis.FamilyNotFound {
		return nil, apperr.ErrFamilyNotFound
	}

	if result == redis.ReplayDetected {
		// ADR 007 §18.3.2：記 presented_jti / current_jti / delta_sec。
		// current_jti / delta_sec 取自被廢 family 的最後狀態（Rotate 於 replay 仍回傳 state）；
		// 若 state 不可用（罕見：state_json 為空）則僅記 presented_jti。
		extra := map[string]any{"presented_jti": presentedJTI}
		if state != nil {
			extra["current_jti"] = state.CurrentJTI
			extra["delta_sec"] = time.Now().Unix() - state.LastRotatedAt
		}
		s.audit.Log(ctx, audit.AuthEvent{
			Type:     audit.EventReplayDetected,
			UserID:   userID,
			FamilyID: fid,
			ClientID: clientID,
			Extra:    extra,
		})
		metrics.AuthReplayDetected.WithLabelValues(clientID).Inc()
		return nil, apperr.ErrReplayDetected
	}

	if state == nil {
		logger.L().Error("family state is nil after rotate", zap.String("result", fmt.Sprintf("%v", result)))
		return nil, apperr.ErrFamilyNotFound
	}

	var refreshJTI string
	if result == redis.GraceHit {
		refreshJTI = state.CurrentJTI
	} else {
		refreshJTI = newJTI
	}

	// 簽發新 access token — TTL 固定用 s.accessTTL（§8.2）
	accessToken, err := s.jwtManager.SignAccess(ctx, jwt.SignAccessParams{
		UserID:   userID,
		UserType: jwt.UserType(state.UserType),
		Role:     jwt.Role(state.Role),
		FamilyID: fid,
		ClientID: clientID,
		JTI:      uuid.New().String(),
		TTL:      s.accessTTL,
	})
	if err != nil {
		logger.L().Error("sign access token failed", zap.Error(err))
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// 簽發新 refresh token — abs_exp 永遠從 state 取，不延長（§8.3.1）
	refreshToken, err := s.jwtManager.SignRefresh(ctx, jwt.SignRefreshParams{
		UserID:      userID,
		UserType:    jwt.UserType(state.UserType),
		FamilyID:    fid,
		ClientID:    clientID,
		JTI:         refreshJTI,
		TTL:         policy.RefreshTTL,
		AbsoluteExp: time.Unix(state.AbsoluteExp, 0),
	})
	if err != nil {
		logger.L().Error("sign refresh token failed", zap.Error(err))
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:     audit.EventTokenRotated,
		UserID:   userID,
		FamilyID: fid,
		ClientID: clientID,
		Extra:    map[string]any{"old_jti": presentedJTI, "new_jti": refreshJTI},
	})

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(s.accessTTL.Seconds()),       // access TTL 秒（§3.5.1）
		RefreshExpiresIn: int(policy.RefreshTTL.Seconds()), // refresh TTL 秒
	}, nil
}

// Logout 廢當前 family + access JTI 入黑名單（§8.9）。
// 若提供 refresh_token，驗 fid 與 access claims 一致（§8.2）。
func (s *authService) Logout(ctx context.Context, in LogoutInput) error {
	// 若提供 refresh_token，驗其有效且 fid 與 access token 一致（§3.5.3 logout 說明）。
	// 任一不符一律回 400 invalid input（對齊 OpenAPI logout 契約：簽章/結構錯與 fid 不符皆 400），
	// 不細分 token_expired / invalid_token——logout 帶錯誤 refresh_token 屬請求格式問題，非認證流程。
	if in.RefreshToken != "" {
		claims, err := s.jwtManager.VerifyRefresh(ctx, in.RefreshToken)
		if err != nil {
			return apperr.ErrInvalidInput
		}
		if claims.FamilyID != in.FamilyID {
			return apperr.ErrInvalidInput
		}
	}

	if _, err := s.familyStore.Revoke(ctx, in.UserID, in.FamilyID); err != nil {
		logger.L().Error("revoke family failed", zap.Error(err))
		return fmt.Errorf("revoke family: %w", err)
	}

	// fail-open：黑名單故障不阻斷登出（§7.3）
	if err := s.blacklist.Add(ctx, in.AccessJTI, in.AccessRemain); err != nil {
		logger.L().Warn("blacklist add failed", zap.Error(err))
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:     audit.EventLogout,
		UserID:   in.UserID,
		FamilyID: in.FamilyID,
	})

	return nil
}

// ListSessions 列出當前 user 全部 family（§8.9）。
func (s *authService) ListSessions(ctx context.Context, userID, currentFID string) ([]SessionInfo, error) {
	states, err := s.familyStore.ListByUser(ctx, userID)
	if err != nil {
		logger.L().Error("list families failed", zap.Error(err))
		return nil, fmt.Errorf("list families: %w", err)
	}

	if len(states) == 0 {
		return []SessionInfo{}, nil
	}

	sessions := make([]SessionInfo, len(states))
	for i, state := range states {
		sessions[i] = SessionInfo{
			FID:           state.FamilyID,
			ClientID:      state.ClientID,
			DeviceLabel:   state.DeviceLabel,
			IPAtLogin:     state.IPAtLogin,
			CreatedAt:     time.Unix(state.CreatedAt, 0),
			LastRotatedAt: time.Unix(state.LastRotatedAt, 0),
			IsCurrent:     state.FamilyID == currentFID,
		}
	}

	return sessions, nil
}

// RevokeSession 撤銷指定 fid（§8.9）。
// targetFID == currentFID → 回 ErrUseLogoutInstead（400 use_logout_instead，請改打 /auth/logout）；
// fid 不存在 → 回 ErrNotFound（404，對齊 OpenAPI / ADR 007）。
func (s *authService) RevokeSession(ctx context.Context, userID, currentFID, targetFID string) error {
	if targetFID == currentFID {
		return apperr.ErrUseLogoutInstead
	}

	found, err := s.familyStore.Revoke(ctx, userID, targetFID)
	if err != nil {
		logger.L().Error("revoke session failed", zap.Error(err))
		return fmt.Errorf("revoke session: %w", err)
	}
	if !found {
		return apperr.ErrNotFound
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:     audit.EventSessionRevoked,
		UserID:   userID,
		FamilyID: targetFID,
		Extra:    map[string]any{"revoked_fid": targetFID, "operator": userID},
	})

	return nil
}

// RevokeAll 撤銷當前 user 所有 family + access JTI 入黑名單（§8.9）。
func (s *authService) RevokeAll(ctx context.Context, userID, currentAccessJTI string, currentAccessRemaining time.Duration) error {
	if err := s.familyStore.RevokeAll(ctx, userID); err != nil {
		logger.L().Error("revoke all sessions failed", zap.Error(err))
		return fmt.Errorf("revoke all sessions: %w", err)
	}

	// fail-open（§7.3）
	if err := s.blacklist.Add(ctx, currentAccessJTI, currentAccessRemaining); err != nil {
		logger.L().Warn("blacklist add failed", zap.Error(err))
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:   audit.EventRevokeAll,
		UserID: userID,
	})

	return nil
}
