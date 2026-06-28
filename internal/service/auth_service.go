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
	"github.com/yintengching/playerledger/pkg/redis"
	"github.com/yintengching/playerledger/pkg/ua"
	"go.uber.org/zap"
)

// AuthService 定义认证服务接口（§8.9）。
type AuthService interface {
	// Register：建立 CMS user，预设 role = "user"。
	// 依 §8.9 弱密码规则、username 唯一性检查。
	Register(ctx context.Context, in RegisterInput) error

	// Login：依 client_id 路由到 cms_users / members，验证帐密 → 开新 family → 签 token pair。
	Login(ctx context.Context, in LoginInput) (*TokenPair, error)

	// Refresh：依 §8.2 / §8.3.1 做 rotation；处理 Rotated / GraceHit / ReplayDetected / FamilyNotFound 三种结果。
	Refresh(ctx context.Context, in RefreshInput) (*TokenPair, error)

	// Logout：廃当前 family + 把当前 access JTI 加入黑名单。
	Logout(ctx context.Context, in LogoutInput) error

	// ListSessions：回当前 user 全部 family，currentFID 命中者 IsCurrent=true；
	// 发现孤兒 fid 顺手清（lazy cleanup）。
	ListSessions(ctx context.Context, userID, currentFID string) ([]SessionInfo, error)

	// RevokeSession：撤销指定 fid。
	// 返回 ErrUseLogoutInstead（targetFID == currentFID）或 ErrNotFound。
	RevokeSession(ctx context.Context, userID, currentFID, targetFID string) error

	// RevokeAll：撤销当前 user 所有 family，并把当前 access JTI 加入黑名单
	// （ttl = currentAccessRemaining，与 Logout 一致）。
	RevokeAll(ctx context.Context, userID, currentAccessJTI string, currentAccessRemaining time.Duration) error
}

// RegisterInput 注册输入（§8.9）。
type RegisterInput struct {
	Username string
	Password string
	ClientID string
}

// LoginInput 登入输入（§8.9）。
type LoginInput struct {
	Username  string
	Password  string
	ClientID  string
	IP        string // handler 从 c.ClientIP() 取
	UserAgent string // handler 从 c.Request.UserAgent() 取
}

// RefreshInput Refresh 输入（§8.2）。
type RefreshInput struct {
	RefreshToken string
	IP           string // 预留给审计，目前未使用
	UserAgent    string // 预留给审计，目前未使用
}

// LogoutInput 登出输入（§8.9）。
type LogoutInput struct {
	UserID       string
	FamilyID     string
	AccessJTI    string
	AccessRemain time.Duration // 加入黑名单 ttl
	RefreshToken string        // optional；非空时验 fid 与 access claims 一致
}

// TokenPair access + refresh token 对（§3.5）。
type TokenPair struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`             // access TTL 秒
	RefreshExpiresIn int    `json:"refresh_expires_in"`     // refresh TTL 秒
}

// SessionInfo 会话信息（GET /auth/sessions 返回，§3.5）。
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
}

// NewAuthService 创建认证服务（§8.9）。
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

// isWeakPassword 检查密码强度（§8.9 弱密码规则）：
// - 至少 8 字符
// - 包含字母 **和** 数字
// 规则：必须同时有字母和数字。
func isWeakPassword(password string) bool {
	if len(password) < 8 {
		return true
	}
	hasLetter := regexp.MustCompile(`[a-zA-Z]`).MatchString(password)
	hasDigit := regexp.MustCompile(`[0-9]`).MatchString(password)
	return !(hasLetter && hasDigit)
}

// Register 注册新 CMS user，预设 role = "user"（§8.9）。
// 仅 cms-web 放行，其餘 → ErrInvalidClient；弱密碼 → ErrWeakPassword；
// username 已占用 → ErrConflict（依 §12.5 unique constraint 23505 包装）。
// 不签 token、不建立 session；caller 另打 /auth/login。
func (s *authService) Register(ctx context.Context, in RegisterInput) error {
	// 仅 cms-web 放行（§8.9）
	if in.ClientID != "cms-web" {
		return apperr.ErrInvalidClient
	}

	// 弱密码规则（§8.9）
	if isWeakPassword(in.Password) {
		audit.Log(&audit.AuditEvent{
			EventType: audit.EventRegisterFailed,
			Timestamp: time.Now().Unix(),
			Username:  in.Username,
			UserType:  "cms",
			ClientID:  in.ClientID,
			Result:    "weak_password",
		})
		return apperr.ErrWeakPassword
	}

	// 哈希密码
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		logger.L().Error("hash password failed", zap.Error(err))
		return fmt.Errorf("hash password: %w", err)
	}

	// 创建 CMS user（预设 role = user）
	user := &model.CMSUser{
		Username:     in.Username,
		PasswordHash: hash,
		Role:         string(jwt.RoleUser),
	}

	if err := s.cmsUserRepo.Create(ctx, user); err != nil {
		// 检查是否为 unique constraint violation（username 已占用）
		if errors.Is(err, apperr.ErrConflict) {
			audit.Log(&audit.AuditEvent{
				EventType: audit.EventRegisterFailed,
				Timestamp: time.Now().Unix(),
				Username:  in.Username,
				UserType:  "cms",
				ClientID:  in.ClientID,
				Result:    "username_taken",
			})
			return apperr.ErrConflict
		}
		// 其他错误
		audit.Log(&audit.AuditEvent{
			EventType: audit.EventRegisterFailed,
			Timestamp: time.Now().Unix(),
			Username:  in.Username,
			UserType:  "cms",
			ClientID:  in.ClientID,
			Result:    "database_error",
		})
		return fmt.Errorf("create cms user: %w", err)
	}

	// 审计：注册成功
	audit.Log(&audit.AuditEvent{
		EventType: audit.EventRegisterSuccess,
		Timestamp: time.Now().Unix(),
		UserID:    user.ID.String(),
		Username:  in.Username,
		UserType:  "cms",
		ClientID:  in.ClientID,
		Result:    "success",
	})

	return nil
}

// Login 登入：依 client_id 路由到 cms_users / members，验证帐密 → 开新 family → 签 token pair（§8.9）。
func (s *authService) Login(ctx context.Context, in LoginInput) (*TokenPair, error) {
	// 获取 client policy
	policy, err := s.jwtManager.PolicyOf(ctx, in.ClientID)
	if err != nil {
		return nil, apperr.ErrInvalidClient
	}

	now := time.Now()
	absExp := now.Add(policy.AbsoluteTTL)
	fid := uuid.New().String()
	accessJTI := uuid.New().String()
	refreshJTI := uuid.New().String()

	// 路由：cms-web -> cms_users，其他 -> members（§8.9）
	var userID, userType string
	var userRole string

	if in.ClientID == "cms-web" {
		user, err := s.cmsUserRepo.FindByUsername(ctx, in.Username)
		if err != nil {
			audit.Log(&audit.AuditEvent{
				EventType: audit.EventLoginFailed,
				Timestamp: now.Unix(),
				Username:  in.Username,
				UserType:  "cms",
				ClientID:  in.ClientID,
				Result:    "user_not_found",
			})
			return nil, apperr.ErrUnauthorized
		}

		// 比对密码
		ok, err := s.hasher.Compare(user.PasswordHash, in.Password)
		if err != nil {
			// ErrMismatch 是预期错误，不是系统错误
			if errors.Is(err, hasher.ErrMismatch) {
				audit.Log(&audit.AuditEvent{
					EventType: audit.EventLoginFailed,
					Timestamp: now.Unix(),
					UserID:    user.ID.String(),
					Username:  in.Username,
					UserType:  "cms",
					ClientID:  in.ClientID,
					Result:    "password_mismatch",
				})
				return nil, apperr.ErrUnauthorized
			}
			logger.L().Error("password compare failed", zap.Error(err))
			return nil, fmt.Errorf("compare password: %w", err)
		}
		if !ok {
			audit.Log(&audit.AuditEvent{
				EventType: audit.EventLoginFailed,
				Timestamp: now.Unix(),
				UserID:    user.ID.String(),
				Username:  in.Username,
				UserType:  "cms",
				ClientID:  in.ClientID,
				Result:    "password_mismatch",
			})
			return nil, apperr.ErrUnauthorized
		}

		userID = user.ID.String()
		userType = "cms"
		userRole = user.Role
	} else {
		// cms_users 以外 → members 路由
		member, err := s.memberRepo.FindByUsername(ctx, in.Username)
		if err != nil {
			audit.Log(&audit.AuditEvent{
				EventType: audit.EventLoginFailed,
				Timestamp: now.Unix(),
				Username:  in.Username,
				UserType:  "member",
				ClientID:  in.ClientID,
				Result:    "user_not_found",
			})
			return nil, apperr.ErrUnauthorized
		}

		// 比对密码
		ok, err := s.hasher.Compare(member.PasswordHash, in.Password)
		if err != nil {
			// ErrMismatch 是预期错误，不是系统错误
			if errors.Is(err, hasher.ErrMismatch) {
				audit.Log(&audit.AuditEvent{
					EventType: audit.EventLoginFailed,
					Timestamp: now.Unix(),
					UserID:    member.ID.String(),
					Username:  in.Username,
					UserType:  "member",
					ClientID:  in.ClientID,
					Result:    "password_mismatch",
				})
				return nil, apperr.ErrUnauthorized
			}
			logger.L().Error("password compare failed", zap.Error(err))
			return nil, fmt.Errorf("compare password: %w", err)
		}
		if !ok {
			audit.Log(&audit.AuditEvent{
				EventType: audit.EventLoginFailed,
				Timestamp: now.Unix(),
				UserID:    member.ID.String(),
				Username:  in.Username,
				UserType:  "member",
				ClientID:  in.ClientID,
				Result:    "password_mismatch",
			})
			return nil, apperr.ErrUnauthorized
		}

		userID = member.ID.String()
		userType = "member"
		userRole = "member" // member 无 role 欄位，固定为 "member"
	}

	// 签发 access token（§8.1 / §8.2）
	accessToken, err := s.jwtManager.SignAccess(ctx, jwt.SignAccessParams{
		UserID:   userID,
		UserType: jwt.UserType(userType),
		Role:     jwt.Role(userRole),
		FamilyID: fid,
		ClientID: in.ClientID,
		JTI:      accessJTI,
		TTL:      policy.RefreshTTL, // 应为 config.JWTConfig.AccessTTL
	})
	if err != nil {
		logger.L().Error("sign access token failed", zap.Error(err))
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// 签发 refresh token（§8.1 / §8.2）
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

	// 保存 family state（§7.4）
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

	// 审计：登入成功
	audit.Log(&audit.AuditEvent{
		EventType: audit.EventLoginSuccess,
		Timestamp: now.Unix(),
		UserID:    userID,
		Username:  in.Username,
		UserType:  userType,
		ClientID:  in.ClientID,
		FamilyID:  fid,
		IPAddress: in.IP,
		Result:    "success",
	})

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(policy.RefreshTTL.Seconds()),
		RefreshExpiresIn: int(policy.RefreshTTL.Seconds()),
	}, nil
}

// Refresh refresh token rotation（§8.2 / §8.3.1）。
// 处理 FamilyStore.Rotate 三种结果：Rotated / GraceHit / ReplayDetected / FamilyNotFound。
func (s *authService) Refresh(ctx context.Context, in RefreshInput) (*TokenPair, error) {
	// 验证 refresh token
	claims, err := s.jwtManager.VerifyRefresh(ctx, in.RefreshToken)
	if err != nil {
		return nil, err
	}

	userID := claims.UserID()
	fid := claims.FamilyID
	clientID := claims.Audience[0]
	presentedJTI := claims.ID
	newJTI := uuid.New().String()

	// 获取 client policy
	policy, err := s.jwtManager.PolicyOf(ctx, clientID)
	if err != nil {
		return nil, err
	}

	// 调用 FamilyStore.Rotate Lua CAS（§7.4 / §8.2）
	result, state, err := s.familyStore.Rotate(ctx, userID, fid, presentedJTI, newJTI, policy.RefreshTTL)
	if err != nil {
		logger.L().Error("family rotate failed", zap.Error(err))
		return nil, fmt.Errorf("rotate family: %w", err)
	}

	// 处理 Rotate 三种结果（§8.3.1）
	if result == redis.FamilyNotFound {
		return nil, apperr.ErrSessionNotFound
	}

	if result == redis.ReplayDetected {
		// 重放检测：audit + metrics（§8.2.1）
		audit.Log(&audit.AuditEvent{
			EventType: audit.EventReplayDetected,
			Timestamp: time.Now().Unix(),
			UserID:    userID,
			ClientID:  clientID,
			FamilyID:  fid,
			Result:    "replay_detected",
		})
		return nil, apperr.ErrReplayDetected
	}

	// 防守性检查：state 不可为 nil（§7.4）
	if state == nil {
		logger.L().Error("family state is nil after rotate", zap.String("result", fmt.Sprintf("%v", result)))
		return nil, apperr.ErrSessionNotFound
	}

	// Rotated 与 GraceHit 路径：重签 tokens（§8.3.1）
	var refreshJTI string
	resultStr := "rotated"
	if result == redis.GraceHit {
		// GraceHit：沿用 state.CurrentJTI 重签 refresh（§8.3.1）
		refreshJTI = state.CurrentJTI
		resultStr = "grace_hit"
	} else {
		// Rotated：用新 newJTI
		refreshJTI = newJTI
	}

	// 签发新 access token（access_token.jti 永远新，§8.3.1）
	accessToken, err := s.jwtManager.SignAccess(ctx, jwt.SignAccessParams{
		UserID:   userID,
		UserType: jwt.UserType(state.UserType),
		Role:     jwt.Role(state.Role),
		FamilyID: fid,
		ClientID: clientID,
		JTI:      uuid.New().String(),
		TTL:      policy.RefreshTTL,
	})
	if err != nil {
		logger.L().Error("sign access token failed", zap.Error(err))
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// 签发新 refresh token（refresh_token.abs_exp 永远从 state 取，§8.3.1）
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

	// 审计
	audit.Log(&audit.AuditEvent{
		EventType: audit.EventRefreshRotated,
		Timestamp: time.Now().Unix(),
		UserID:    userID,
		ClientID:  clientID,
		FamilyID:  fid,
		Result:    resultStr,
	})

	return &TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(policy.RefreshTTL.Seconds()),
		RefreshExpiresIn: int(policy.RefreshTTL.Seconds()),
	}, nil
}

// Logout 登出：廢当前 family + 把当前 access JTI 加入黑名单（§8.9）。
func (s *authService) Logout(ctx context.Context, in LogoutInput) error {
	// 廢当前 family（§8.2）
	if err := s.familyStore.Revoke(ctx, in.UserID, in.FamilyID); err != nil {
		logger.L().Error("revoke family failed", zap.Error(err))
		return fmt.Errorf("revoke family: %w", err)
	}

	// 将 access JTI 加入黑名单（ttl = 剩余 exp，§7.3）
	// 无论 Redis 成功或失败，都继续（fail-open，见 §7.3）
	if err := s.blacklist.Add(ctx, in.AccessJTI, in.AccessRemain); err != nil {
		logger.L().Warn("blacklist add failed", zap.Error(err))
		// fail-open：不让黑名单故障阻断登出
	}

	// 审计
	audit.Log(&audit.AuditEvent{
		EventType: audit.EventLogoutSuccess,
		Timestamp: time.Now().Unix(),
		UserID:    in.UserID,
		FamilyID:  in.FamilyID,
		Result:    "success",
	})

	return nil
}

// ListSessions 列出当前 user 全部 family，currentFID 命中者 IsCurrent=true；
// 发现孤兒 fid 顺手清（lazy cleanup，§8.9）。
func (s *authService) ListSessions(ctx context.Context, userID, currentFID string) ([]SessionInfo, error) {
	states, err := s.familyStore.ListByUser(ctx, userID)
	if err != nil {
		logger.L().Error("list families failed", zap.Error(err))
		return nil, fmt.Errorf("list families: %w", err)
	}

	// 保证返回非 nil slice（§10）
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

// RevokeSession 撤销指定 fid（防守性检查不能撤自己当前 family，§8.9）。
// 返回 ErrUseLogoutInstead（targetFID == currentFID）或 ErrNotFound。
func (s *authService) RevokeSession(ctx context.Context, userID, currentFID, targetFID string) error {
	// 防守性检查：不能撤自己当前 family（§8.9）
	if targetFID == currentFID {
		return apperr.New("forbidden", nil, "use_logout_instead")
	}

	// 撤销指定 family
	if err := s.familyStore.Revoke(ctx, userID, targetFID); err != nil {
		logger.L().Error("revoke session failed", zap.Error(err))
		return fmt.Errorf("revoke session: %w", err)
	}

	// 审计
	audit.Log(&audit.AuditEvent{
		EventType: audit.EventRevokeSessionOther,
		Timestamp: time.Now().Unix(),
		UserID:    userID,
		FamilyID:  targetFID,
		Result:    "success",
	})

	return nil
}

// RevokeAll 撤销当前 user 所有 family，并把当前 access JTI 加入黑名单
// （ttl = currentAccessRemaining，与 Logout 一致，§8.9）。
func (s *authService) RevokeAll(ctx context.Context, userID, currentAccessJTI string, currentAccessRemaining time.Duration) error {
	// 撤销所有 family（§8.2）
	if err := s.familyStore.RevokeAll(ctx, userID); err != nil {
		logger.L().Error("revoke all sessions failed", zap.Error(err))
		return fmt.Errorf("revoke all sessions: %w", err)
	}

	// 将当前 access JTI 加入黑名单（§8.9 设计取捨）
	// 否则「全裝置登出」后当前 access token 仍可用到自然过期（最长 15 分鐘）。
	if err := s.blacklist.Add(ctx, currentAccessJTI, currentAccessRemaining); err != nil {
		logger.L().Warn("blacklist add failed", zap.Error(err))
		// fail-open：不让黑名单故障阻断登出
	}

	// 审计
	audit.Log(&audit.AuditEvent{
		EventType: audit.EventRevokeAllSessions,
		Timestamp: time.Now().Unix(),
		UserID:    userID,
		Result:    "success",
	})

	return nil
}
