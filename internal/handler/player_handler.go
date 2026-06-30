package handler

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/yintengching/playerledger/internal/dto"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/ctxkey"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/jwt"
)

// PlayerHandler 玩家查詢 HTTP handler（players-api.md §3）。
type PlayerHandler struct {
	svc service.PlayerService
}

func NewPlayerHandler(svc service.PlayerService) *PlayerHandler {
	return &PlayerHandler{svc: svc}
}

// phoneNormalizer 去除 phone 中的空白 / - / ( / )（保留前導 +）。
var phoneNormalizer = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")

// ─── GET /api/cms/players ─────────────────────────────────────────────────────

func (h *PlayerHandler) Search(c *gin.Context) {
	in := service.PlayerSearchInput{}
	hasCond := false

	// player_id — 精確；非 UUID → 400
	if v := strings.TrimSpace(c.Query("player_id")); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		in.PlayerID = &id
		hasCond = true
	}

	// external_id — 精確
	if v := strings.TrimSpace(c.Query("external_id")); v != "" {
		if utf8.RuneCountInString(v) > 64 {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		in.ExternalID = &v
		hasCond = true
	}

	// display_name — trim → NFC，前綴
	if v := strings.TrimSpace(c.Query("display_name")); v != "" {
		v = norm.NFC.String(v)
		if utf8.RuneCountInString(v) > 64 {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		in.DisplayName = &v
		hasCond = true
	}

	// email — trim → lowercase，前綴
	if v := strings.TrimSpace(c.Query("email")); v != "" {
		v = strings.ToLower(v)
		if utf8.RuneCountInString(v) > 255 {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		in.Email = &v
		hasCond = true
	}

	// phone — trim → 去符號，精確
	if v := strings.TrimSpace(c.Query("phone")); v != "" {
		v = phoneNormalizer.Replace(v)
		if v != "" {
			if utf8.RuneCountInString(v) > 32 {
				httpx.WriteError(c, http.StatusBadRequest, "invalid input")
				return
			}
			in.Phone = &v
			hasCond = true
		}
	}

	// 無任何有效搜尋條件 → 400（不允許無條件全表瀏覽）
	if !hasCond {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// limit — 1..50，缺省 20；超出 → 400
	limit := 20
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		limit = n
	}
	in.Limit = limit

	// cursor — opaque，原樣傳給 service 解碼（失敗時 service 回 ErrInvalidInput）
	if v := strings.TrimSpace(c.Query("cursor")); v != "" {
		in.Cursor = &v
	}

	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := ctxkey.SetActor(c.Request.Context(), ctxkey.Actor{UserID: claims.UserID(), Role: string(claims.Role)})

	out, err := h.svc.Search(ctx, in)
	if err != nil {
		HandleError(c, err)
		return
	}

	mask := claims.Role != jwt.RoleAdmin
	c.JSON(http.StatusOK, OK(c, dto.PlayerSearchResult{
		Players:    dto.FromMemberList(out.Players, mask),
		NextCursor: out.NextCursor,
	}))
}

// ─── GET /api/cms/players/:id ─────────────────────────────────────────────────

func (h *PlayerHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := ctxkey.SetActor(c.Request.Context(), ctxkey.Actor{UserID: claims.UserID(), Role: string(claims.Role)})

	member, err := h.svc.Get(ctx, id)
	if err != nil {
		HandleError(c, err)
		return
	}

	mask := claims.Role != jwt.RoleAdmin
	c.JSON(http.StatusOK, OK(c, dto.FromMember(member, mask)))
}

// ─── GET /api/cms/players/:id/deposit-summary ─────────────────────────────────

func (h *PlayerHandler) DepositSummary(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := ctxkey.SetActor(c.Request.Context(), ctxkey.Actor{UserID: claims.UserID(), Role: string(claims.Role)})

	// 彙總無 PII，全 CMS staff（含 viewer）皆回完整值，不遮罩。
	out, err := h.svc.DepositSummary(ctx, id)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, dto.FromDepositSummary(out)))
}
