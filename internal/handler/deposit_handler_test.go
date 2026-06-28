package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/internal/service"
	pkgjwt "github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
)

// ─── Fake DepositService ──────────────────────────────────────────────────────

type fakeDepositService struct {
	records map[uuid.UUID]*model.DepositRecord
	// 可注入錯誤供特定測試使用
	createErr error
	updateErr error
}

func newFakeDepositService() *fakeDepositService {
	return &fakeDepositService{records: make(map[uuid.UUID]*model.DepositRecord)}
}

func (s *fakeDepositService) Create(_ context.Context, input service.CreateDepositInput) (*model.DepositRecord, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	opID := input.OperatorID
	rec := &model.DepositRecord{
		ID:            uuid.New(),
		PlayerID:      input.PlayerID,
		PlayerName:    "player",
		Amount:        input.Amount,
		Currency:      input.Currency,
		Status:        model.DepositStatusPending,
		PaymentMethod: input.PaymentMethod,
		OperatorID:    &opID,
		OperatorIP:    input.OperatorIP,
		InternalNote:  input.InternalNote,
		DisplayNote:   input.DisplayNote,
		ReferenceNo:   input.ReferenceNo,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	s.records[rec.ID] = rec
	return rec, nil
}

func (s *fakeDepositService) Get(_ context.Context, id uuid.UUID) (*model.DepositRecord, error) {
	rec, ok := s.records[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return rec, nil
}

func (s *fakeDepositService) List(_ context.Context, _ repository.DepositRecordFilter) ([]*model.DepositRecord, int64, error) {
	var result []*model.DepositRecord
	for _, r := range s.records {
		result = append(result, r)
	}
	return result, int64(len(result)), nil
}

func (s *fakeDepositService) Update(_ context.Context, id uuid.UUID, input service.UpdateDepositInput) (*model.DepositRecord, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	rec, ok := s.records[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	if input.NewStatus != nil {
		rec.Status = *input.NewStatus
	}
	if input.InternalNote != nil {
		rec.InternalNote = *input.InternalNote
	}
	if input.DisplayNote != nil {
		rec.DisplayNote = *input.DisplayNote
	}
	return rec, nil
}

func (s *fakeDepositService) ListByPlayer(_ context.Context, playerID uuid.UUID, _ repository.PlayerDepositFilter) ([]*model.DepositRecord, int64, error) {
	var result []*model.DepositRecord
	for _, r := range s.records {
		if r.PlayerID == playerID {
			result = append(result, r)
		}
	}
	return result, int64(len(result)), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

var logOnce = func() bool {
	logger.Init(config.LogConfig{Format: "console", Level: "debug", Service: "test"}, "dev") //nolint:errcheck
	return true
}()

// injectClaims 注入測試用 JWT claims（跳過真實簽名）。
func injectClaims(claims *pkgjwt.AccessClaims) gin.HandlerFunc {
	return func(c *gin.Context) {
		pkgjwt.SetClaims(c, claims)
		c.Next()
	}
}

// setupDepositCMSRouter 建立含 CMS claims 注入的測試 router。
func setupDepositCMSRouter(t *testing.T, svc service.DepositService) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	_ = logOnce

	operatorID := uuid.New().String()
	claims := &pkgjwt.AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: operatorID},
		UserType:         pkgjwt.UserTypeCMS,
	}

	r := gin.New()
	r.Use(logger.RequestID())
	r.Use(injectClaims(claims))

	h := NewDepositHandler(svc)
	cmsGroup := r.Group("/api/cms/deposit-records")
	cmsGroup.POST("", h.Create)
	cmsGroup.GET("", h.List)
	cmsGroup.GET("/:id", h.Get)
	cmsGroup.PATCH("/:id", h.UpdateStatus)

	return r, operatorID
}

// setupDepositMemberRouter 建立含 member claims 注入的測試 router。
func setupDepositMemberRouter(t *testing.T, svc service.DepositService, playerID string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	_ = logOnce

	claims := &pkgjwt.AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: playerID},
		UserType:         pkgjwt.UserTypeMember,
	}

	r := gin.New()
	r.Use(logger.RequestID())
	r.Use(injectClaims(claims))

	h := NewDepositHandler(svc)
	r.GET("/api/v1/me/deposit-records", h.ListMine)

	return r
}

func doRequest(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, path, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedFakeDeposit(svc *fakeDepositService, status model.DepositStatus, playerID uuid.UUID) *model.DepositRecord {
	opID := uuid.New()
	rec := &model.DepositRecord{
		ID:            uuid.New(),
		PlayerID:      playerID,
		PlayerName:    "player",
		Amount:        500,
		Currency:      "TWD",
		Status:        status,
		PaymentMethod: model.PaymentMethodManual,
		OperatorID:    &opID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	svc.records[rec.ID] = rec
	return rec
}

// ─── POST /api/cms/deposit-records ────────────────────────────────────────────

// TestDepositHandler_Create_Success_Returns201
func TestDepositHandler_Create_Success_Returns201(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{
		"player_id":      uuid.New().String(),
		"amount":         1000,
		"payment_method": "manual",
	}

	w := doRequest(r, http.MethodPost, "/api/cms/deposit-records", body)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "pending", data["status"])
	assert.Equal(t, float64(1000), data["amount"])
}

// TestDepositHandler_Create_MissingPlayerID_Returns400
func TestDepositHandler_Create_MissingPlayerID_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{
		"amount":         100,
		"payment_method": "manual",
	}

	w := doRequest(r, http.MethodPost, "/api/cms/deposit-records", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDepositHandler_Create_InvalidPaymentMethod_Returns400
func TestDepositHandler_Create_InvalidPaymentMethod_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{
		"player_id":      uuid.New().String(),
		"amount":         100,
		"payment_method": "bitcoin", // invalid
	}

	w := doRequest(r, http.MethodPost, "/api/cms/deposit-records", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDepositHandler_Create_PlayerNotFound_Returns404
func TestDepositHandler_Create_PlayerNotFound_Returns404(t *testing.T) {
	svc := newFakeDepositService()
	svc.createErr = apperr.ErrNotFound
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{
		"player_id":      uuid.New().String(),
		"amount":         100,
		"payment_method": "manual",
	}

	w := doRequest(r, http.MethodPost, "/api/cms/deposit-records", body)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestDepositHandler_Create_ReferenceNoConflict_Returns409
func TestDepositHandler_Create_ReferenceNoConflict_Returns409(t *testing.T) {
	svc := newFakeDepositService()
	svc.createErr = apperr.ErrReferenceNoConflict
	r, _ := setupDepositCMSRouter(t, svc)

	ref := "TXN-001"
	body := map[string]any{
		"player_id":      uuid.New().String(),
		"amount":         100,
		"payment_method": "manual",
		"reference_no":   ref,
	}

	w := doRequest(r, http.MethodPost, "/api/cms/deposit-records", body)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// ─── GET /api/cms/deposit-records ────────────────────────────────────────────

// TestDepositHandler_List_Success_ReturnsArray
func TestDepositHandler_List_Success_ReturnsArray(t *testing.T) {
	svc := newFakeDepositService()
	seedFakeDeposit(svc, model.DepositStatusPending, uuid.New())
	seedFakeDeposit(svc, model.DepositStatusCompleted, uuid.New())
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].([]any)
	assert.Len(t, data, 2)
}

// TestDepositHandler_List_PageSizeOver100_Returns400
func TestDepositHandler_List_PageSizeOver100_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records?page_size=101", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDepositHandler_List_InvalidSort_Returns400
func TestDepositHandler_List_InvalidSort_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records?sort=invalid", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDepositHandler_List_InvalidStatus_Returns400
func TestDepositHandler_List_InvalidStatus_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records?status=unknown_status", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── GET /api/cms/deposit-records/:id ────────────────────────────────────────

// TestDepositHandler_Get_Success_Returns200
func TestDepositHandler_Get_Success_Returns200(t *testing.T) {
	svc := newFakeDepositService()
	rec := seedFakeDeposit(svc, model.DepositStatusPending, uuid.New())
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records/"+rec.ID.String(), nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, rec.ID.String(), data["id"])
}

// TestDepositHandler_Get_NotFound_Returns404
func TestDepositHandler_Get_NotFound_Returns404(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records/"+uuid.New().String(), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestDepositHandler_Get_InvalidID_Returns400
func TestDepositHandler_Get_InvalidID_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r, _ := setupDepositCMSRouter(t, svc)

	w := doRequest(r, http.MethodGet, "/api/cms/deposit-records/not-a-uuid", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── PATCH /api/cms/deposit-records/:id ──────────────────────────────────────

// TestDepositHandler_UpdateStatus_Success_Returns200
func TestDepositHandler_UpdateStatus_Success_Returns200(t *testing.T) {
	svc := newFakeDepositService()
	rec := seedFakeDeposit(svc, model.DepositStatusPending, uuid.New())
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{"status": "completed"}
	w := doRequest(r, http.MethodPatch, "/api/cms/deposit-records/"+rec.ID.String(), body)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "completed", data["status"])
}

// TestDepositHandler_UpdateStatus_ClearNote_Returns200（三態：null → 清空）
func TestDepositHandler_UpdateStatus_ClearNote_Returns200(t *testing.T) {
	svc := newFakeDepositService()
	note := "old note"
	rec := seedFakeDeposit(svc, model.DepositStatusPending, uuid.New())
	rec.DisplayNote = &note
	r, _ := setupDepositCMSRouter(t, svc)

	// 傳 null 值（JSON `null`）
	rawBody := []byte(`{"display_note":null}`)
	req, _ := http.NewRequest(http.MethodPatch, "/api/cms/deposit-records/"+rec.ID.String(), bytes.NewBuffer(rawBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestDepositHandler_UpdateStatus_EmptyBody_Returns400
func TestDepositHandler_UpdateStatus_EmptyBody_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	rec := seedFakeDeposit(svc, model.DepositStatusPending, uuid.New())
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{} // 沒有提供任何欄位
	w := doRequest(r, http.MethodPatch, "/api/cms/deposit-records/"+rec.ID.String(), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDepositHandler_UpdateStatus_InvalidTransition_Returns422
func TestDepositHandler_UpdateStatus_InvalidTransition_Returns422(t *testing.T) {
	svc := newFakeDepositService()
	rec := seedFakeDeposit(svc, model.DepositStatusPending, uuid.New())
	svc.updateErr = apperr.ErrInvalidTransition
	r, _ := setupDepositCMSRouter(t, svc)

	body := map[string]any{"status": "refunded"}
	w := doRequest(r, http.MethodPatch, "/api/cms/deposit-records/"+rec.ID.String(), body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// ─── GET /api/v1/me/deposit-records ──────────────────────────────────────────

// TestDepositHandler_ListMine_Success_ReturnsOnlyOwnRecords
func TestDepositHandler_ListMine_Success_ReturnsOnlyOwnRecords(t *testing.T) {
	svc := newFakeDepositService()
	playerID := uuid.New()
	seedFakeDeposit(svc, model.DepositStatusPending, playerID)
	seedFakeDeposit(svc, model.DepositStatusCompleted, playerID)
	seedFakeDeposit(svc, model.DepositStatusPending, uuid.New()) // 他人記錄

	r := setupDepositMemberRouter(t, svc, playerID.String())
	w := doRequest(r, http.MethodGet, "/api/v1/me/deposit-records", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].([]any)
	assert.Len(t, data, 2)
}

// TestDepositHandler_ListMine_HidesInternalNote
func TestDepositHandler_ListMine_HidesInternalNote(t *testing.T) {
	svc := newFakeDepositService()
	playerID := uuid.New()
	rec := seedFakeDeposit(svc, model.DepositStatusPending, playerID)
	note := "secret note"
	rec.InternalNote = &note

	r := setupDepositMemberRouter(t, svc, playerID.String())
	w := doRequest(r, http.MethodGet, "/api/v1/me/deposit-records", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].([]any)
	require.Len(t, data, 1)
	item := data[0].(map[string]any)
	// PublicDTO 不含 internal_note
	_, hasInternalNote := item["internal_note"]
	assert.False(t, hasInternalNote)
}

// TestDepositHandler_ListMine_PageSizeOver50_Returns400
func TestDepositHandler_ListMine_PageSizeOver50_Returns400(t *testing.T) {
	svc := newFakeDepositService()
	r := setupDepositMemberRouter(t, svc, uuid.New().String())
	w := doRequest(r, http.MethodGet, "/api/v1/me/deposit-records?page_size=51", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
