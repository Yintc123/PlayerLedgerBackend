package handler

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/dto"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/jwt"
)

// CMSUserHandler CMS 內部人員管理 HTTP handler（cms-users-api，5 endpoints）。
type CMSUserHandler struct {
	svc service.CMSUserService
}

func NewCMSUserHandler(svc service.CMSUserService) *CMSUserHandler {
	return &CMSUserHandler{svc: svc}
}

// validCMSRoles role 白名單（§4.1 / §4.3）。
var validCMSRoles = map[string]bool{"admin": true, "user": true, "viewer": true}

// validCMSUserSorts sort 白名單（§4.1）。
var validCMSUserSorts = map[string]bool{
	"created_at": true, "-created_at": true,
	"username": true, "-username": true,
}

// ─── GET /api/cms/users ──────────────────────────────────────────────────────

func (h *CMSUserHandler) List(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	page, ok := parseIntQuery(c, "page", 1)
	if !ok {
		return
	}
	pageSize, ok := parseIntQuery(c, "page_size", 20)
	if !ok {
		return
	}
	if pageSize > 100 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// role[] 白名單（可重複做 OR 篩選）
	var roleFilter []string
	for _, r := range c.QueryArray("role") {
		if !validCMSRoles[r] {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		roleFilter = append(roleFilter, r)
	}

	// username_like：若提供，最少 2 字元（§4.1）
	usernameLike := c.Query("username_like")
	if usernameLike != "" && len([]rune(usernameLike)) < 2 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// sort 白名單
	sortParam := c.DefaultQuery("sort", "-created_at")
	if !validCMSUserSorts[sortParam] {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// include_deleted：僅 admin 有效，non-admin 靜默忽略（§4.1）
	includeDeleted := c.Query("include_deleted") == "true" && claims.Role == jwt.RoleAdmin

	opts := repository.ListCMSUsersOptions{
		Page:           page,
		PageSize:       pageSize,
		RoleFilter:     roleFilter,
		UsernameLike:   usernameLike,
		IncludeDeleted: includeDeleted,
		Sort:           sortParam,
	}

	users, total, err := h.svc.List(c.Request.Context(), opts)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OKList(c, dto.FromCMSUserList(users), page, pageSize, total))
}

// ─── GET /api/cms/users/:id ──────────────────────────────────────────────────

func (h *CMSUserHandler) Get(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	includeDeleted := c.Query("include_deleted") == "true" && claims.Role == jwt.RoleAdmin

	user, err := h.svc.Get(c.Request.Context(), id.String(), includeDeleted)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, dto.FromCMSUser(user)))
}

// ─── PATCH /api/cms/users/:id ────────────────────────────────────────────────

type updateCMSUserRequest struct {
	Username *string `json:"username"`
	Role     *string `json:"role"`
}

func (h *CMSUserHandler) Update(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	var req updateCMSUserRequest
	if !decodeStrictJSON(c, &req) {
		return
	}

	// minProperties: 至少一個欄位
	if req.Username == nil && req.Role == nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}
	if !validUsernamePtr(req.Username) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}
	if req.Role != nil && !validCMSRoles[*req.Role] {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	updated, err := h.svc.Update(c.Request.Context(), claims.UserID(), id.String(), service.UpdateCMSUserInput{
		Username: req.Username,
		Role:     req.Role,
	})
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, dto.FromCMSUser(updated)))
}

// ─── DELETE /api/cms/users/:id ───────────────────────────────────────────────

func (h *CMSUserHandler) Delete(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	if err := h.svc.SoftDelete(c.Request.Context(), claims.UserID(), id.String()); err != nil {
		HandleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// ─── PATCH /api/cms/users/me ─────────────────────────────────────────────────

type updateSelfRequest struct {
	Username        *string `json:"username"`
	CurrentPassword *string `json:"current_password"`
	NewPassword     *string `json:"new_password"`
}

func (h *CMSUserHandler) UpdateSelf(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req updateSelfRequest
	if !decodeStrictJSON(c, &req) {
		return
	}

	if req.Username == nil && req.CurrentPassword == nil && req.NewPassword == nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}
	if !validUsernamePtr(req.Username) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}
	// OpenAPI schema 長度約束 → 400 invalid input（密碼複雜度規則仍由 service 回 422 weak_password）。
	if !validLenPtr(req.CurrentPassword, 1, 256) || !validLenPtr(req.NewPassword, 8, 256) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	updated, err := h.svc.UpdateSelf(c.Request.Context(), claims.UserID(), service.UpdateSelfInput{
		Username:        req.Username,
		CurrentPassword: req.CurrentPassword,
		NewPassword:     req.NewPassword,
	})
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, dto.FromCMSUser(updated)))
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// decodeStrictJSON 以 DisallowUnknownFields 解析 body（對齊 OpenAPI additionalProperties:false）。
// 失敗（未知欄位 / 型別錯 / 語法錯）一律回 400 並回傳 false。
func decodeStrictJSON(c *gin.Context, dst any) bool {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return false
	}
	return true
}

// validUsernamePtr 驗證 username 長度 3–64（nil 視為合法，代表不改）。
func validUsernamePtr(u *string) bool {
	if u == nil {
		return true
	}
	n := len([]rune(*u))
	return n >= 3 && n <= 64
}

// validLenPtr 驗證字串長度落在 [min, max]（nil 視為合法，代表欄位缺席）。
// 密碼類欄位以位元組長度計（與 OpenAPI minLength/maxLength 對齊）。
func validLenPtr(s *string, min, max int) bool {
	if s == nil {
		return true
	}
	n := len(*s)
	return n >= min && n <= max
}
