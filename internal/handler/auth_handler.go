package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
)

type AuthHandler struct {
	authService service.AuthService
}

// NewAuthHandler 创建认证 handler。
func NewAuthHandler(authService service.AuthService) *AuthHandler {
	return &AuthHandler{
		authService: authService,
	}
}

// RegisterRequest 注册请求。
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Password string `json:"password" binding:"required,min=8,max=256"`
	ClientID string `json:"client_id" binding:"required"`
}

// Register POST /auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_input")
		return
	}

	if err := h.authService.Register(c.Request.Context(), req.Username, req.Password, req.ClientID); err != nil {
		httpx.HandleError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

// LoginRequest 登入请求。
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	ClientID string `json:"client_id" binding:"required"`
}

// Login POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_input")
		return
	}

	tokenPair, err := h.authService.Login(c.Request.Context(), req.Username, req.Password, req.ClientID)
	if err != nil {
		httpx.HandleError(c, err)
		return
	}

	requestID := logger.GetRequestID(c)
	c.JSON(http.StatusOK, OK(requestID, tokenPair))
}

// RefreshRequest refresh 请求。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_input")
		return
	}

	tokenPair, err := h.authService.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		httpx.HandleError(c, err)
		return
	}

	requestID := logger.GetRequestID(c)
	c.JSON(http.StatusOK, OK(requestID, tokenPair))
}

// Logout POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.authService.Logout(c.Request.Context(), claims.UserID(), claims.FamilyID, claims.ID); err != nil {
		httpx.HandleError(c, err)
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

	sessions, err := h.authService.ListSessions(c.Request.Context(), claims.UserID())
	if err != nil {
		httpx.HandleError(c, err)
		return
	}

	requestID := logger.GetRequestID(c)
	c.JSON(http.StatusOK, OK(requestID, sessions))
}

// RevokeSession DELETE /auth/sessions/:fid
func (h *AuthHandler) RevokeSession(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	fid := c.Param("fid")
	if err := h.authService.RevokeSession(c.Request.Context(), claims.UserID(), claims.FamilyID, fid); err != nil {
		httpx.HandleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// RevokeAll POST /auth/sessions/revoke-all
func (h *AuthHandler) RevokeAll(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.authService.RevokeAll(c.Request.Context(), claims.UserID()); err != nil {
		httpx.HandleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
