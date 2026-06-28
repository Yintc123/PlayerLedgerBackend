package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/jwt"
)

// AuthHandler 認證 handler。
type AuthHandler struct {
	authService service.AuthService
}

// NewAuthHandler 建立認證 handler。
func NewAuthHandler(authService service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

// RegisterRequest 註冊請求（§3.5.3）。
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Password string `json:"password" binding:"required,min=8,max=256"`
	ClientID string `json:"client_id" binding:"required"`
}

// Register POST /auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	if err := h.authService.Register(c.Request.Context(), service.RegisterInput{
		Username: req.Username,
		Password: req.Password,
		ClientID: req.ClientID,
	}); err != nil {
		HandleError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

// LoginRequest 登入請求（§3.5.3）。
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	ClientID string `json:"client_id" binding:"required"`
}

// Login POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	tokenPair, err := h.authService.Login(c.Request.Context(), service.LoginInput{
		Username:  req.Username,
		Password:  req.Password,
		ClientID:  req.ClientID,
		IP:        c.ClientIP(),
		UserAgent: c.Request.Header.Get("User-Agent"),
	})
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, tokenPair))
}

// RefreshRequest refresh 請求（§3.5.3）。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	tokenPair, err := h.authService.Refresh(c.Request.Context(), service.RefreshInput{
		RefreshToken: req.RefreshToken,
		IP:           c.ClientIP(),
		UserAgent:    c.Request.Header.Get("User-Agent"),
	})
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, tokenPair))
}

// LogoutRequest 登出請求（optional body，§3.5.3）。
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	// 嘗試解析可選 body（忽略解析失敗；body 為 optional）
	var req LogoutRequest
	_ = c.ShouldBindJSON(&req)

	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining < 0 {
		remaining = 0
	}

	if err := h.authService.Logout(c.Request.Context(), service.LogoutInput{
		UserID:       claims.UserID(),
		FamilyID:     claims.FamilyID,
		AccessJTI:    claims.ID,
		AccessRemain: remaining,
		RefreshToken: req.RefreshToken,
	}); err != nil {
		HandleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// ListSessions GET /auth/sessions
func (h *AuthHandler) ListSessions(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	sessions, err := h.authService.ListSessions(c.Request.Context(), claims.UserID(), claims.FamilyID)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, sessions))
}

// RevokeSession DELETE /auth/sessions/:fid
func (h *AuthHandler) RevokeSession(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	targetFID := c.Param("fid")
	if err := h.authService.RevokeSession(c.Request.Context(), claims.UserID(), claims.FamilyID, targetFID); err != nil {
		HandleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// RevokeAllSessions POST /auth/sessions/revoke-all
func (h *AuthHandler) RevokeAllSessions(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining < 0 {
		remaining = 0
	}

	if err := h.authService.RevokeAll(c.Request.Context(), claims.UserID(), claims.ID, remaining); err != nil {
		HandleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
