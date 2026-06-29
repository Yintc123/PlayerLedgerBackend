package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/dto"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/httpx"
	"github.com/yintengching/playerledger/pkg/jwt"
)

// DepositHandler 儲值紀錄 HTTP handler（5 endpoints）。
type DepositHandler struct {
	svc service.DepositService
}

func NewDepositHandler(svc service.DepositService) *DepositHandler {
	return &DepositHandler{svc: svc}
}

// ─── POST /api/cms/deposit-records ───────────────────────────────────────────

type createDepositRequest struct {
	PlayerID      string  `json:"player_id"      binding:"required,uuid"`
	Amount        int64   `json:"amount"         binding:"required,min=1"`
	Currency      string  `json:"currency"       binding:"omitempty,len=3"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
	InternalNote  *string `json:"internal_note"  binding:"omitempty,max=2000"`
	DisplayNote   *string `json:"display_note"   binding:"omitempty,max=500"`
	ReferenceNo   *string `json:"reference_no"   binding:"omitempty,max=128"`
}

func (h *DepositHandler) Create(c *gin.Context) {
	var req createDepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		HandleError(c, err)
		return
	}

	playerID, err := uuid.Parse(req.PlayerID)
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	pm := model.PaymentMethod(req.PaymentMethod)
	switch pm {
	case model.PaymentMethodBankTransfer, model.PaymentMethodCreditCard,
		model.PaymentMethodManual, model.PaymentMethodConvenienceStore, model.PaymentMethodEWallet:
	default:
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = "TWD"
	}

	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	operatorID, err := uuid.Parse(claims.UserID())
	if err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "internal server error")
		return
	}
	ip := c.ClientIP()

	rec, err := h.svc.Create(c.Request.Context(), service.CreateDepositInput{
		PlayerID:      playerID,
		Amount:        req.Amount,
		Currency:      currency,
		PaymentMethod: pm,
		InternalNote:  req.InternalNote,
		DisplayNote:   req.DisplayNote,
		ReferenceNo:   req.ReferenceNo,
		OperatorID:    operatorID,
		OperatorIP:    &ip,
	})
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusCreated, OK(c, dto.FromDepositRecord(rec)))
}

// ─── GET /api/cms/deposit-records ────────────────────────────────────────────

// validDepositStatuses 供 handler 驗證 status 查詢參數。
var validDepositStatuses = map[string]model.DepositStatus{
	"pending":   model.DepositStatusPending,
	"completed": model.DepositStatusCompleted,
	"failed":    model.DepositStatusFailed,
	"cancelled": model.DepositStatusCancelled,
	"refunded":  model.DepositStatusRefunded,
}

var validPaymentMethods = map[string]model.PaymentMethod{
	"bank_transfer":     model.PaymentMethodBankTransfer,
	"credit_card":       model.PaymentMethodCreditCard,
	"manual":            model.PaymentMethodManual,
	"convenience_store": model.PaymentMethodConvenienceStore,
	"e_wallet":          model.PaymentMethodEWallet,
}

var validDepositSorts = map[string]bool{
	"-created_at": true,
	"created_at":  true,
	"-amount":     true,
	"amount":      true,
}

func (h *DepositHandler) List(c *gin.Context) {
	page := parseIntQuery(c, "page", 1)
	pageSize := parseIntQuery(c, "page_size", 20)
	if pageSize > 100 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// 解析多值 status 篩選
	var statuses []model.DepositStatus
	for _, s := range c.QueryArray("status") {
		ds, ok := validDepositStatuses[s]
		if !ok {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		statuses = append(statuses, ds)
	}

	// 解析多值 payment_method 篩選
	var paymentMethods []model.PaymentMethod
	for _, pm := range c.QueryArray("payment_method") {
		p, ok := validPaymentMethods[pm]
		if !ok {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		paymentMethods = append(paymentMethods, p)
	}

	// sort 白名單驗證
	sortParam := c.DefaultQuery("sort", "-created_at")
	if !validDepositSorts[sortParam] {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// 解析可選 player_id
	var playerID *uuid.UUID
	if pidStr := c.Query("player_id"); pidStr != "" {
		pid, err := uuid.Parse(pidStr)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		playerID = &pid
	}

	startDate, ok := parseDateQuery(c, "start_date", false)
	if !ok {
		return
	}
	endDate, ok := parseDateQuery(c, "end_date", true)
	if !ok {
		return
	}
	if !validateDateRange(c, startDate, endDate) {
		return
	}

	filter := repository.DepositRecordFilter{
		PlayerID:      playerID,
		Status:        statuses,
		PaymentMethod: paymentMethods,
		StartDate:     startDate,
		EndDate:       endDate,
		Sort:          sortParam,
		Page:          page,
		PageSize:      pageSize,
	}

	records, total, err := h.svc.List(c.Request.Context(), filter)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OKList(c, dto.FromDepositRecordList(records), page, pageSize, total))
}

// ─── GET /api/cms/deposit-records/:id ────────────────────────────────────────

func (h *DepositHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	rec, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, dto.FromDepositRecord(rec)))
}

// ─── PATCH /api/cms/deposit-records/:id ──────────────────────────────────────

func (h *DepositHandler) UpdateStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	// 用 map[string]json.RawMessage 實現三態語意（absent / null / value）
	var rawBody map[string]json.RawMessage
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		HandleError(c, err)
		return
	}

	input := service.UpdateDepositInput{}

	if v, ok := rawBody["status"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		ds := model.DepositStatus(s)
		if _, valid := validDepositStatuses[s]; !valid {
			httpx.WriteError(c, http.StatusBadRequest, "invalid input")
			return
		}
		input.NewStatus = &ds
	}

	internalNote, ok := parseTriStateNote(c, rawBody, "internal_note", 2000)
	if !ok {
		return
	}
	input.InternalNote = internalNote

	displayNote, ok := parseTriStateNote(c, rawBody, "display_note", 500)
	if !ok {
		return
	}
	input.DisplayNote = displayNote

	// 至少一個欄位必須提供
	if input.NewStatus == nil && input.InternalNote == nil && input.DisplayNote == nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	updated, err := h.svc.Update(c.Request.Context(), id, input)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OK(c, dto.FromDepositRecord(updated)))
}

// ─── GET /api/me/deposit-records ─────────────────────────────────────────────

func (h *DepositHandler) ListMine(c *gin.Context) {
	claims, ok := jwt.GetClaims(c)
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	// member token 的 claims.sub = members.id（§4.5）
	playerID, err := uuid.Parse(claims.UserID())
	if err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "internal server error")
		return
	}

	page := parseIntQuery(c, "page", 1)
	pageSize := parseIntQuery(c, "page_size", 20)
	if pageSize > 50 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return
	}

	startDate, ok := parseDateQuery(c, "start_date", false)
	if !ok {
		return
	}
	endDate, ok := parseDateQuery(c, "end_date", true)
	if !ok {
		return
	}
	if !validateDateRange(c, startDate, endDate) {
		return
	}

	filter := repository.PlayerDepositFilter{
		StartDate: startDate,
		EndDate:   endDate,
		Page:      page,
		PageSize:  pageSize,
	}

	records, total, err := h.svc.ListByPlayer(c.Request.Context(), playerID, filter)
	if err != nil {
		HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, OKList(c, dto.FromDepositRecordPublicList(records), page, pageSize, total))
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func parseIntQuery(c *gin.Context, key string, defaultVal int) int {
	s := c.Query(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	return v
}

// parseDateQuery 解析 YYYY-MM-DD 日期。endOfDay=true 時設為 23:59:59 UTC。
// 回傳 (nil, true) 表示欄位缺席；回傳 (nil, false) 表示格式錯誤且已寫入 400 回應。
func parseDateQuery(c *gin.Context, key string, endOfDay bool) (*time.Time, bool) {
	s := c.Query(key)
	if s == "" {
		return nil, true
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return nil, false
	}
	if endOfDay {
		t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
	}
	return &t, true
}

// validateDateRange 確保 end_date 不早於 start_date（§4.2）。
// 兩者皆有值且 end < start 時回 false 並寫入 400；其餘情況回 true。
func validateDateRange(c *gin.Context, start, end *time.Time) bool {
	if start != nil && end != nil && end.Before(*start) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return false
	}
	return true
}

// parseTriStateNote 解析三態備註欄位（缺席 / null / 值）並驗證長度上限。
// 回傳值對應 repository 三態語意：
//
//	nil           → 欄位缺席，不修改
//	&(*string)nil → 明確 null，清空
//	&&"text"      → 設定新值
//
// ok=false 時已寫入 400 回應（JSON 型別錯誤或超過長度上限）。
func parseTriStateNote(c *gin.Context, raw map[string]json.RawMessage, key string, maxLen int) (**string, bool) {
	v, present := raw[key]
	if !present {
		return nil, true
	}
	if string(v) == "null" {
		var nilStr *string
		return &nilStr, true
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return nil, false
	}
	if len(s) > maxLen {
		httpx.WriteError(c, http.StatusBadRequest, "invalid input")
		return nil, false
	}
	sp := &s
	return &sp, true
}
