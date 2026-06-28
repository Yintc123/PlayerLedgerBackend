package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/pkg/audit"
)

// CreateDepositInput handler 提供的原始輸入；PlayerName 不在此，由 service 查詢後填入 entity。
type CreateDepositInput struct {
	PlayerID      uuid.UUID
	Amount        int64
	Currency      string
	PaymentMethod model.PaymentMethod
	InternalNote  *string
	DisplayNote   *string
	ReferenceNo   *string
	OperatorID    uuid.UUID // 從 access token claims.sub 取得
	OperatorIP    *string   // 從 c.ClientIP() 取得
}

// UpdateDepositInput handler 提供的更新輸入；三態語意同 repository.UpdateDepositInput。
// service 內部將此型別映射至 repository.UpdateDepositInput，handler 無需 import repository 包。
type UpdateDepositInput struct {
	NewStatus    *model.DepositStatus
	InternalNote **string
	DisplayNote  **string
}

// DepositService 儲值紀錄業務介面。
type DepositService interface {
	Create(ctx context.Context, input CreateDepositInput) (*model.DepositRecord, error)
	Get(ctx context.Context, id uuid.UUID) (*model.DepositRecord, error)
	List(ctx context.Context, f repository.DepositRecordFilter) ([]*model.DepositRecord, int64, error)
	// Update 驗證 CanTransition，更新後依結果寫 audit log（status_changed 或 note_updated）。
	// input 為 service 包內的 UpdateDepositInput；service 負責映射至 repository.UpdateDepositInput。
	Update(ctx context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error)
	// ListByPlayer player_id 由 caller 從 token 取得後傳入，service 不自行讀 token。
	ListByPlayer(ctx context.Context, playerID uuid.UUID, f repository.PlayerDepositFilter) ([]*model.DepositRecord, int64, error)
}

type depositService struct {
	depositRepo repository.DepositRecordRepository
	memberRepo  repository.MemberRepository
	audit       audit.Logger
}

func NewDepositService(
	depositRepo repository.DepositRecordRepository,
	memberRepo repository.MemberRepository,
	auditLogger audit.Logger,
) DepositService {
	return &depositService{
		depositRepo: depositRepo,
		memberRepo:  memberRepo,
		audit:       auditLogger,
	}
}

func (s *depositService) Create(ctx context.Context, input CreateDepositInput) (*model.DepositRecord, error) {
	member, err := s.memberRepo.FindByID(ctx, input.PlayerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("find member for deposit: %w", err)
	}

	operatorID := input.OperatorID
	rec := &model.DepositRecord{
		PlayerID:      input.PlayerID,
		PlayerName:    member.Username,
		Amount:        input.Amount,
		Currency:      input.Currency,
		Status:        model.DepositStatusPending,
		PaymentMethod: input.PaymentMethod,
		OperatorID:    &operatorID,
		OperatorIP:    input.OperatorIP,
		InternalNote:  input.InternalNote,
		DisplayNote:   input.DisplayNote,
		ReferenceNo:   input.ReferenceNo,
	}

	if err := s.depositRepo.Create(ctx, rec); err != nil {
		if errors.Is(err, apperr.ErrReferenceNoConflict) {
			return nil, apperr.ErrReferenceNoConflict
		}
		return nil, fmt.Errorf("create deposit: %w", err)
	}

	s.audit.Log(ctx, audit.AuthEvent{
		Type:   audit.EventDepositCreated,
		UserID: input.OperatorID.String(),
		Extra: map[string]any{
			"deposit_id":     rec.ID.String(),
			"player_id":      rec.PlayerID.String(),
			"amount":         rec.Amount,
			"currency":       rec.Currency,
			"payment_method": string(rec.PaymentMethod),
			"operator_id":    input.OperatorID.String(),
		},
	})

	return rec, nil
}

func (s *depositService) Get(ctx context.Context, id uuid.UUID) (*model.DepositRecord, error) {
	return s.depositRepo.FindByID(ctx, id)
}

func (s *depositService) List(ctx context.Context, f repository.DepositRecordFilter) ([]*model.DepositRecord, int64, error) {
	return s.depositRepo.List(ctx, f)
}

func (s *depositService) Update(ctx context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error) {
	current, err := s.depositRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.NewStatus != nil {
		if !model.CanTransition(current.Status, *input.NewStatus) {
			return nil, apperr.ErrInvalidTransition
		}
	}

	// 在呼叫 Update 前快照 fromStatus，避免 fake/pointer 實作回傳同一物件導致值被覆寫
	fromStatus := current.Status

	repoInput := repository.UpdateDepositInput{
		NewStatus:    input.NewStatus,
		InternalNote: input.InternalNote,
		DisplayNote:  input.DisplayNote,
	}

	updated, err := s.depositRepo.Update(ctx, id, repoInput)
	if err != nil {
		return nil, err
	}

	operatorID := ""
	if updated.OperatorID != nil {
		operatorID = updated.OperatorID.String()
	}

	// 若 status 實際改變 → deposit.status_changed；否則 → deposit.note_updated（§8）
	if input.NewStatus != nil {
		s.audit.Log(ctx, audit.AuthEvent{
			Type:   audit.EventDepositStatusChanged,
			UserID: operatorID,
			Extra: map[string]any{
				"deposit_id":  id.String(),
				"from_status": string(fromStatus),
				"to_status":   string(*input.NewStatus),
				"operator_id": operatorID,
			},
		})
	} else {
		s.audit.Log(ctx, audit.AuthEvent{
			Type:   audit.EventDepositNoteUpdated,
			UserID: operatorID,
			Extra: map[string]any{
				"deposit_id":  id.String(),
				"operator_id": operatorID,
			},
		})
	}

	return updated, nil
}

func (s *depositService) ListByPlayer(ctx context.Context, playerID uuid.UUID, f repository.PlayerDepositFilter) ([]*model.DepositRecord, int64, error) {
	return s.depositRepo.ListByPlayer(ctx, playerID, f)
}
