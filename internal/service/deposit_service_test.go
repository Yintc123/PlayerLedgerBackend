package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
)

// ─── fakes ───────────────────────────────────────────────────────────────────

type fakeDepositRepo struct {
	records   map[uuid.UUID]*model.DepositRecord
	createErr error
}

func newFakeDepositRepo() *fakeDepositRepo {
	return &fakeDepositRepo{records: make(map[uuid.UUID]*model.DepositRecord)}
}

func (r *fakeDepositRepo) Create(_ context.Context, rec *model.DepositRecord) error {
	if r.createErr != nil {
		return r.createErr
	}
	if rec.ID == (uuid.UUID{}) {
		rec.ID = uuid.New()
	}
	r.records[rec.ID] = rec
	return nil
}

func (r *fakeDepositRepo) FindByID(_ context.Context, id uuid.UUID) (*model.DepositRecord, error) {
	rec, ok := r.records[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return rec, nil
}

func (r *fakeDepositRepo) List(_ context.Context, _ repository.DepositRecordFilter) ([]*model.DepositRecord, int64, error) {
	var result []*model.DepositRecord
	for _, rec := range r.records {
		result = append(result, rec)
	}
	return result, int64(len(result)), nil
}

func (r *fakeDepositRepo) Update(_ context.Context, id uuid.UUID, input repository.UpdateDepositInput) (*model.DepositRecord, error) {
	rec, ok := r.records[id]
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

func (r *fakeDepositRepo) ListByPlayer(_ context.Context, playerID uuid.UUID, _ repository.PlayerDepositFilter) ([]*model.DepositRecord, int64, error) {
	var result []*model.DepositRecord
	for _, rec := range r.records {
		if rec.PlayerID == playerID {
			result = append(result, rec)
		}
	}
	return result, int64(len(result)), nil
}

type fakeMemberRepoDeposit struct {
	members map[uuid.UUID]*model.Member
}

func newFakeMemberRepoDeposit() *fakeMemberRepoDeposit {
	return &fakeMemberRepoDeposit{members: make(map[uuid.UUID]*model.Member)}
}

func (r *fakeMemberRepoDeposit) FindByUsername(_ context.Context, username string) (*model.Member, error) {
	for _, m := range r.members {
		if m.Username == username {
			return m, nil
		}
	}
	return nil, apperr.ErrNotFound
}

func (r *fakeMemberRepoDeposit) FindByID(_ context.Context, id uuid.UUID) (*model.Member, error) {
	m, ok := r.members[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return m, nil
}

// captureAudit 記錄寫入的 audit event，供測試斷言。
type captureAudit struct {
	events []audit.AuthEvent
}

func (a *captureAudit) Log(_ context.Context, e audit.AuthEvent) { a.events = append(a.events, e) }
func (a *captureAudit) Sync() error                              { return nil }

// ─── helpers ─────────────────────────────────────────────────────────────────

func newDepositSvc(depositRepo *fakeDepositRepo, memberRepo *fakeMemberRepoDeposit, auditLog audit.Logger) DepositService {
	return NewDepositService(depositRepo, memberRepo, auditLog)
}

func seedRecord(repo *fakeDepositRepo, status model.DepositStatus) *model.DepositRecord {
	opID := uuid.New()
	rec := &model.DepositRecord{
		ID:            uuid.New(),
		PlayerID:      uuid.New(),
		PlayerName:    "player",
		Amount:        1000,
		Currency:      "TWD",
		Status:        status,
		PaymentMethod: model.PaymentMethodManual,
		OperatorID:    &opID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	repo.records[rec.ID] = rec
	return rec
}

// ─── Create tests ─────────────────────────────────────────────────────────────

// TestDepositService_Create_PlayerNotFound_ReturnsErrNotFound player_id 不存在 → 404
func TestDepositService_Create_PlayerNotFound_ReturnsErrNotFound(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	memberRepo := newFakeMemberRepoDeposit()
	svc := newDepositSvc(depositRepo, memberRepo, audit.NewNopLogger())

	_, err := svc.Create(context.Background(), CreateDepositInput{
		PlayerID:      uuid.New(),
		Amount:        1000,
		Currency:      "TWD",
		PaymentMethod: model.PaymentMethodManual,
		OperatorID:    uuid.New(),
	})

	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestDepositService_Create_ReferenceNoConflict_ReturnsErrReferenceNoConflict reference_no 重複 → 409
func TestDepositService_Create_ReferenceNoConflict_ReturnsErrReferenceNoConflict(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	depositRepo.createErr = apperr.ErrReferenceNoConflict

	memberRepo := newFakeMemberRepoDeposit()
	playerID := uuid.New()
	memberRepo.members[playerID] = &model.Member{Base: model.Base{ID: playerID}, Username: "alice"}

	svc := newDepositSvc(depositRepo, memberRepo, audit.NewNopLogger())
	ref := "TXN-001"

	_, err := svc.Create(context.Background(), CreateDepositInput{
		PlayerID:      playerID,
		Amount:        1000,
		Currency:      "TWD",
		PaymentMethod: model.PaymentMethodManual,
		OperatorID:    uuid.New(),
		ReferenceNo:   &ref,
	})

	assert.ErrorIs(t, err, apperr.ErrReferenceNoConflict)
}

// TestDepositService_Create_Success_SetsPlayerNameSnapshot 建立成功時 PlayerName 為快照
func TestDepositService_Create_Success_SetsPlayerNameSnapshot(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	memberRepo := newFakeMemberRepoDeposit()
	playerID := uuid.New()
	memberRepo.members[playerID] = &model.Member{Base: model.Base{ID: playerID}, Username: "alice"}

	svc := newDepositSvc(depositRepo, memberRepo, audit.NewNopLogger())

	rec, err := svc.Create(context.Background(), CreateDepositInput{
		PlayerID:      playerID,
		Amount:        500,
		Currency:      "TWD",
		PaymentMethod: model.PaymentMethodBankTransfer,
		OperatorID:    uuid.New(),
	})

	require.NoError(t, err)
	assert.Equal(t, "alice", rec.PlayerName)
	assert.Equal(t, model.DepositStatusPending, rec.Status)
}

// TestDepositService_Create_EmitsDepositCreatedAudit 建立成功後寫 deposit.created audit
func TestDepositService_Create_EmitsDepositCreatedAudit(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	memberRepo := newFakeMemberRepoDeposit()
	playerID := uuid.New()
	memberRepo.members[playerID] = &model.Member{Base: model.Base{ID: playerID}, Username: "bob"}
	a := &captureAudit{}

	svc := newDepositSvc(depositRepo, memberRepo, a)
	_, err := svc.Create(context.Background(), CreateDepositInput{
		PlayerID:      playerID,
		Amount:        100,
		Currency:      "TWD",
		PaymentMethod: model.PaymentMethodManual,
		OperatorID:    uuid.New(),
	})

	require.NoError(t, err)
	require.Len(t, a.events, 1)
	assert.Equal(t, audit.EventDepositCreated, a.events[0].Type)
}

// ─── Update / transition tests ────────────────────────────────────────────────

// TestDepositService_Update_IllegalTransition_PendingToRefunded pending → refunded 非法
func TestDepositService_Update_IllegalTransition_PendingToRefunded(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusPending)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusRefunded
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_CompletedToFailed completed → failed 非法
func TestDepositService_Update_IllegalTransition_CompletedToFailed(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusCompleted)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusFailed
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_CompletedToCancelled completed → cancelled 非法
func TestDepositService_Update_IllegalTransition_CompletedToCancelled(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusCompleted)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusCancelled
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_CompletedToPending completed → pending 非法
func TestDepositService_Update_IllegalTransition_CompletedToPending(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusCompleted)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusPending
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_FailedToCompleted failed → completed 非法
func TestDepositService_Update_IllegalTransition_FailedToCompleted(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusFailed)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusCompleted
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_FailedToRefunded failed → refunded 非法
func TestDepositService_Update_IllegalTransition_FailedToRefunded(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusFailed)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusRefunded
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_CancelledToCompleted cancelled → completed 非法
func TestDepositService_Update_IllegalTransition_CancelledToCompleted(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusCancelled)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusCompleted
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_IllegalTransition_RefundedToCompleted refunded → completed 非法
func TestDepositService_Update_IllegalTransition_RefundedToCompleted(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusRefunded)
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusCompleted
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrInvalidTransition)
}

// TestDepositService_Update_ValidTransition_PendingToCompleted_EmitsStatusChangedAudit
func TestDepositService_Update_ValidTransition_PendingToCompleted_EmitsStatusChangedAudit(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusPending)
	a := &captureAudit{}
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), a)

	to := model.DepositStatusCompleted
	updated, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{NewStatus: &to})

	require.NoError(t, err)
	assert.Equal(t, model.DepositStatusCompleted, updated.Status)
	require.Len(t, a.events, 1)
	assert.Equal(t, audit.EventDepositStatusChanged, a.events[0].Type)
	assert.Equal(t, "pending", a.events[0].Extra["from_status"])
	assert.Equal(t, "completed", a.events[0].Extra["to_status"])
}

// TestDepositService_Update_NoteOnly_EmitsNoteUpdatedAudit 純備註更新觸發 note_updated
func TestDepositService_Update_NoteOnly_EmitsNoteUpdatedAudit(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	rec := seedRecord(depositRepo, model.DepositStatusPending)
	a := &captureAudit{}
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), a)

	note := "updated note"
	notePtr := &note
	_, err := svc.Update(context.Background(), rec.ID, UpdateDepositInput{InternalNote: &notePtr})

	require.NoError(t, err)
	require.Len(t, a.events, 1)
	assert.Equal(t, audit.EventDepositNoteUpdated, a.events[0].Type)
}

// TestDepositService_Update_NotFound_ReturnsErrNotFound
func TestDepositService_Update_NotFound_ReturnsErrNotFound(t *testing.T) {
	depositRepo := newFakeDepositRepo()
	svc := newDepositSvc(depositRepo, newFakeMemberRepoDeposit(), audit.NewNopLogger())

	to := model.DepositStatusCompleted
	_, err := svc.Update(context.Background(), uuid.New(), UpdateDepositInput{NewStatus: &to})
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
