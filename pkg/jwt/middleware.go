package jwt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/logger"
	"github.com/yintengching/playerledger/pkg/metrics"
	"go.uber.org/zap"
)

// AccessTokenBlacklist 短期黑名單接口（§7.3）。
// 僅用於「強制踢人」場景（管理員手動踢、改密碼、family revoked 連帶）。
// 日常 access token 驗證不查黑名單以維持 stateless 性能；
// 只在需要立刻撤銷時加入，TTL = access token 剩餘 exp 秒數。
type AccessTokenBlacklist interface {
	Add(ctx context.Context, jti string, ttl time.Duration) error
	IsBlacklisted(ctx context.Context, jti string) (bool, error)
}

// AuthMiddleware 驗證 access token，將 claims 注入 context（透過 SetClaims）。
// blacklist 為「強制踢人」用的短期黑名單，非常態查詢；hot path 預期黑名單 miss。
//
// 處理流程：
//  1. 從 Authorization header 取 "Bearer <token>"
//     - 規範化：去除前後空白；prefix 比對「Bearer 」case-insensitive，僅接受單一空白
//     - header 缺 / 前綴錯 / token 部分為空 → 401 `unauthorized`
//  2. jwtManager.VerifyAccess(token) — 依錯誤 sentinel 對應 HTTP error code：
//     - ErrTokenExpired                       → 401 `token_expired`   （前端 retry refresh）
//     - ErrInvalidToken（含 alg/iss/aud/簽章/nbf/iat）→ 401 `invalid_token`   （前端走 login）
//     - 其他不預期 error                       → 401 `unauthorized`
//  3. blacklist.IsBlacklisted(ctx, claims.ID):
//     - (true, nil)  → 401 `session_revoked`（middleware 內直接寫 error code，不過 HandleError；見 §12.4）
//     - (false, nil) → 通過
//     - (false, err) → **fail-open**：log warn + metrics.AuthBlacklistErrors.Inc() + 通過
//  4. SetClaims(c, claims) + c.Next()
func AuthMiddleware(jwtManager Manager, blacklist AccessTokenBlacklist) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 步驟 1：取 Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		// 規範化：分割為 prefix 與 token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		token := parts[1]
		if token == "" {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		// 步驟 2：驗證 token
		claims, err := jwtManager.VerifyAccess(c.Request.Context(), token)
		var errCode string
		if err != nil {
			switch {
			case errors.Is(err, ErrTokenExpired):
				errCode = "token_expired"
			case errors.Is(err, ErrInvalidToken):
				errCode = "invalid_token"
			default:
				errCode = "unauthorized"
			}
			httpx.WriteError(c, http.StatusUnauthorized, errCode)
			c.Abort()
			return
		}

		// 步驟 3：檢查黑名單（fail-open，§7.3）
		hit, err := blacklist.IsBlacklisted(c.Request.Context(), claims.ID)
		if err != nil {
			// Redis 故障：fail-open
			logger.L().Warn("blacklist check failed, allowing request",
				zap.String("request_id", logger.GetRequestID(c)),
				zap.Error(err),
			)
			metrics.AuthBlacklistErrors.Inc()
		} else if hit {
			// 黑名單命中：強制踢人（見 §12.4）
			httpx.WriteError(c, http.StatusUnauthorized, "session_revoked")
			c.Abort()
			return
		}

		// 步驟 4：注入 claims
		SetClaims(c, claims)
		c.Next()
	}
}

// RequireRole 驗證 token role 是否符合，需接在 AuthMiddleware 之後。
//
// 注意：
//  1. 不呼叫 HandleError（internal/handler），以避免 pkg/jwt ↔ internal/handler 循環依賴。
//     直接內嵌 401 / 403 回應格式。
//  2. 使用 GetClaims（typed accessor），避免字串 key 散落。
func RequireRole(roles ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := GetClaims(c)
		if !ok {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}
		for _, r := range roles {
			if claims.Role == r {
				c.Next()
				return
			}
		}
		httpx.WriteError(c, http.StatusForbidden, "forbidden")
		c.Abort()
	}
}

// RequireOwnership 確保 URL param 指定的目標 ID 屬於當前登入者。
// 對 UserType == cms 自動放行（由 RequireRole 控制權限）。
// 對 UserType == member 嚴格比對 claims.UserID() == c.Param(paramName)，不符回 403。
//
// Sanity check：path param 若不存在（拼錯 paramName 或 router 沒掛對應 :param），
// c.Param 回 ""，與任何 UserID 比較都不等 → 永遠 403，會造成「靜默全 forbidden」的詭異 bug。
// 故啟動期間記 warn log 提醒（不 panic，避免 init 失敗無法上線）。
func RequireOwnership(paramName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := GetClaims(c)
		if !ok {
			httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}
		if claims.UserType == UserTypeCMS {
			c.Next()
			return
		}
		target := c.Param(paramName)
		if target == "" {
			// 配置錯誤：router 沒對應 :paramName。記 error 並 403 拒絕（fail-closed）。
			logger.L().Error("RequireOwnership misconfigured — path param missing",
				zap.String("request_id", logger.GetRequestID(c)),
				zap.String("path", c.FullPath()),
				zap.String("param_name", paramName),
			)
			httpx.WriteError(c, http.StatusForbidden, "forbidden")
			c.Abort()
			return
		}
		if claims.UserID() != target {
			httpx.WriteError(c, http.StatusForbidden, "forbidden")
			c.Abort()
			return
		}
		c.Next()
	}
}
