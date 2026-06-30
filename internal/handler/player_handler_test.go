package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/service"
	pkgjwt "github.com/yintengching/playerledger/pkg/jwt"
	"github.com/yintengching/playerledger/pkg/logger"
)

// ─── Fake PlayerService ───────────────────────────────────────────────────────

type fakePlayerService struct {
	members    []*model.Member
	out        service.PlayerSearchOutput
	searchErr  error
	getErr     error
	lastInput  service.PlayerSearchInput
	summaryOut *service.DepositSummaryOutput
	summaryErr error
}

func (s *fakePlayerService) Search(_ context.Context, in service.PlayerSearchInput) (service.PlayerSearchOutput, error) {
	s.lastInput = in
	if s.searchErr != nil {
		return service.PlayerSearchOutput{}, s.searchErr
	}
	return s.out, nil
}

func (s *fakePlayerService) Get(_ context.Context, id uuid.UUID) (*model.Member, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for _, m := range s.members {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, apperr.ErrNotFound
}

func (s *fakePlayerService) DepositSummary(_ context.Context, id uuid.UUID) (*service.DepositSummaryOutput, error) {
	if s.summaryErr != nil {
		return nil, s.summaryErr
	}
	if s.summaryOut != nil {
		return s.summaryOut, nil
	}
	return &service.DepositSummaryOutput{PlayerID: id, Totals: []service.CurrencyTotals{}}, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func setupPlayerRouter(t *testing.T, svc service.PlayerService, role pkgjwt.Role) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	_ = logOnce

	claims := &pkgjwt.AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.New().String()},
		UserType:         pkgjwt.UserTypeCMS,
		Role:             role,
	}

	r := gin.New()
	r.Use(logger.RequestID())
	r.Use(injectClaims(claims))

	h := NewPlayerHandler(svc)
	g := r.Group("/api/cms")
	g.GET("/players", h.Search)
	g.GET("/players/:id", h.Get)
	g.GET("/players/:id/deposit-summary", h.DepositSummary)
	return r
}

func sampleMember() *model.Member {
	ext := "EXT-1"
	email := "wang@example.com"
	phone := "+886912345678"
	return &model.Member{
		Base:        model.Base{ID: uuid.New(), CreatedAt: time.Date(2026, 5, 1, 8, 30, 0, 0, time.UTC)},
		Username:    "wang",
		DisplayName: "王小明",
		ExternalID:  &ext,
		Email:       &email,
		Phone:       &phone,
		Status:      model.MemberStatusActive,
	}
}

func playersOf(resp map[string]any) []any {
	return resp["data"].(map[string]any)["players"].([]any)
}

// ─── Search ──────────────────────────────────────────────────────────────────

func TestPlayerHandler_Search_Success_Returns200(t *testing.T) {
	svc := &fakePlayerService{out: service.PlayerSearchOutput{Players: []*model.Member{sampleMember()}}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name=王", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	players := playersOf(resp)
	require.Len(t, players, 1)
	assert.Equal(t, "王小明", players[0].(map[string]any)["display_name"])
}

func TestPlayerHandler_Search_Viewer_遮罩EmailPhone(t *testing.T) {
	svc := &fakePlayerService{out: service.PlayerSearchOutput{Players: []*model.Member{sampleMember()}}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleViewer)

	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name=王", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	p := playersOf(resp)[0].(map[string]any)
	assert.Equal(t, "w***@example.com", p["email"])
	assert.Equal(t, "+886***5678", p["phone"])
	// 其餘欄位不遮罩
	assert.Equal(t, "王小明", p["display_name"])
	assert.Equal(t, "EXT-1", p["external_id"])
}

func TestPlayerHandler_Search_Admin_不遮罩(t *testing.T) {
	svc := &fakePlayerService{out: service.PlayerSearchOutput{Players: []*model.Member{sampleMember()}}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name=王", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	p := playersOf(resp)[0].(map[string]any)
	assert.Equal(t, "wang@example.com", p["email"])
	assert.Equal(t, "+886912345678", p["phone"])
}

func TestPlayerHandler_Search_空條件_Returns400(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlayerHandler_Search_空字串參數_視為未提供_Returns400(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	// email= 與 display_name 空白，正規化後皆空 → 無有效條件 → 400
	w := doRequest(r, http.MethodGet, "/api/cms/players?email=&display_name=%20%20", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlayerHandler_Search_超過maxLength_Returns400(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	long := ""
	for i := 0; i < 65; i++ {
		long += "a"
	}
	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name="+long, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlayerHandler_Search_limit超範圍_Returns400(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name=王&limit=51", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlayerHandler_Search_非法cursor_Returns400(t *testing.T) {
	svc := &fakePlayerService{searchErr: apperr.ErrInvalidInput}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name=王&cursor=!!!", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlayerHandler_Search_正規化_email小寫_phone去符號(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players?email=ABC@X.COM&phone=%2B886%20912-345(678)", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.lastInput.Email)
	assert.Equal(t, "abc@x.com", *svc.lastInput.Email)
	require.NotNil(t, svc.lastInput.Phone)
	assert.Equal(t, "+886912345678", *svc.lastInput.Phone)
}

// ─── Get ─────────────────────────────────────────────────────────────────────

func TestPlayerHandler_Get_Success_Returns200(t *testing.T) {
	m := sampleMember()
	svc := &fakePlayerService{members: []*model.Member{m}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+m.ID.String(), nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, m.ID.String(), data["player_id"])
}

func TestPlayerHandler_Get_Viewer_遮罩(t *testing.T) {
	m := sampleMember()
	svc := &fakePlayerService{members: []*model.Member{m}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleViewer)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+m.ID.String(), nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "w***@example.com", resp["data"].(map[string]any)["email"])
}

func TestPlayerHandler_Get_NotFound_Returns404(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+uuid.New().String(), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPlayerHandler_Get_InvalidUUID_Returns400(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/not-a-uuid", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── 權限（RequireUserType）─────────────────────────────────────────────────

func TestPlayerHandler_Search_Member_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = logOnce
	svc := &fakePlayerService{}

	claims := &pkgjwt.AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.New().String()},
		UserType:         pkgjwt.UserTypeMember,
		Role:             pkgjwt.RoleMember,
	}
	r := gin.New()
	r.Use(logger.RequestID())
	r.Use(injectClaims(claims))
	h := NewPlayerHandler(svc)
	g := r.Group("/api/cms").Use(pkgjwt.RequireUserType(pkgjwt.UserTypeCMS))
	g.GET("/players", h.Search)

	w := doRequest(r, http.MethodGet, "/api/cms/players?display_name=王", nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ─── DepositSummary（players-deposit-summary-api）────────────────────────────

func TestPlayerHandler_DepositSummary_Success_Returns200NoMeta(t *testing.T) {
	id := uuid.New()
	svc := &fakePlayerService{summaryOut: &service.DepositSummaryOutput{
		PlayerID: id,
		Totals: []service.CurrencyTotals{{
			Currency: "TWD", CompletedCount: 12, CompletedAmount: 24800,
			RefundedCount: 1, RefundedAmount: 1200, FailedCount: 2, RefundRate: 0.0462,
		}},
	}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+id.String()+"/deposit-summary", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	_, hasMeta := resp["meta"]
	assert.False(t, hasMeta, "彙總非分頁，回應不應含 meta")
	data := resp["data"].(map[string]any)
	totals := data["totals_by_currency"].([]any)
	require.Len(t, totals, 1)
	tw := totals[0].(map[string]any)
	assert.Equal(t, "TWD", tw["currency"])
	assert.Equal(t, float64(24800), tw["completed_amount"])
	assert.InDelta(t, 0.0462, tw["refund_rate"], 1e-9)
}

func TestPlayerHandler_DepositSummary_InvalidUUID_Returns400(t *testing.T) {
	svc := &fakePlayerService{}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/not-a-uuid/deposit-summary", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlayerHandler_DepositSummary_NotFound_Returns404(t *testing.T) {
	svc := &fakePlayerService{summaryErr: apperr.ErrNotFound}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+uuid.New().String()+"/deposit-summary", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPlayerHandler_DepositSummary_Viewer_Returns200不遮罩(t *testing.T) {
	id := uuid.New()
	svc := &fakePlayerService{summaryOut: &service.DepositSummaryOutput{PlayerID: id, Totals: []service.CurrencyTotals{}}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleViewer)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+id.String()+"/deposit-summary", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPlayerHandler_DepositSummary_空彙總_序列化為陣列與null(t *testing.T) {
	id := uuid.New()
	svc := &fakePlayerService{summaryOut: &service.DepositSummaryOutput{PlayerID: id, Totals: []service.CurrencyTotals{}}}
	r := setupPlayerRouter(t, svc, pkgjwt.RoleAdmin)

	w := doRequest(r, http.MethodGet, "/api/cms/players/"+id.String()+"/deposit-summary", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"totals_by_currency":[]`)
	assert.Contains(t, w.Body.String(), `"first_topup_at":null`)
	assert.Contains(t, w.Body.String(), `"lifetime_days":null`)
}
