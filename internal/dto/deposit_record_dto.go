package dto

import (
	"time"

	"github.com/yintengching/playerledger/internal/model"
)

// DepositRecordDTO CMS 端完整欄位（含 internal_note、operator_*）。
type DepositRecordDTO struct {
	ID            string  `json:"id"`
	PlayerID      string  `json:"player_id"`
	PlayerName    string  `json:"player_name"`
	Amount        int64   `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	PaymentMethod string  `json:"payment_method"`
	OperatorID    *string `json:"operator_id,omitempty"`
	OperatorIP    *string `json:"operator_ip,omitempty"`
	InternalNote  *string `json:"internal_note,omitempty"`
	DisplayNote   *string `json:"display_note,omitempty"`
	ReferenceNo   *string `json:"reference_no,omitempty"`
	CreatedAt     string  `json:"created_at"` // RFC 3339
	UpdatedAt     string  `json:"updated_at"` // RFC 3339
}

// DepositRecordPublicDTO 玩家端（隱藏 internal_note、operator_*、reference_no、player_id）。
type DepositRecordPublicDTO struct {
	ID            string  `json:"id"`
	Amount        int64   `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	PaymentMethod string  `json:"payment_method"`
	DisplayNote   *string `json:"display_note,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

// FromDepositRecord 從 model 轉換為 CMS DTO。
func FromDepositRecord(r *model.DepositRecord) DepositRecordDTO {
	dto := DepositRecordDTO{
		ID:            r.ID.String(),
		PlayerID:      r.PlayerID.String(),
		PlayerName:    r.PlayerName,
		Amount:        r.Amount,
		Currency:      r.Currency,
		Status:        string(r.Status),
		PaymentMethod: string(r.PaymentMethod),
		OperatorIP:    r.OperatorIP,
		InternalNote:  r.InternalNote,
		DisplayNote:   r.DisplayNote,
		ReferenceNo:   r.ReferenceNo,
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     r.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if r.OperatorID != nil {
		s := r.OperatorID.String()
		dto.OperatorID = &s
	}
	return dto
}

// FromDepositRecordPublic 從 model 轉換為玩家端 DTO。
func FromDepositRecordPublic(r *model.DepositRecord) DepositRecordPublicDTO {
	return DepositRecordPublicDTO{
		ID:            r.ID.String(),
		Amount:        r.Amount,
		Currency:      r.Currency,
		Status:        string(r.Status),
		PaymentMethod: string(r.PaymentMethod),
		DisplayNote:   r.DisplayNote,
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// FromDepositRecordList 批量轉換為 CMS DTO slice（保證非 nil）。
func FromDepositRecordList(records []*model.DepositRecord) []DepositRecordDTO {
	if len(records) == 0 {
		return []DepositRecordDTO{}
	}
	dtos := make([]DepositRecordDTO, len(records))
	for i, r := range records {
		dtos[i] = FromDepositRecord(r)
	}
	return dtos
}

// FromDepositRecordPublicList 批量轉換為玩家端 DTO slice（保證非 nil）。
func FromDepositRecordPublicList(records []*model.DepositRecord) []DepositRecordPublicDTO {
	if len(records) == 0 {
		return []DepositRecordPublicDTO{}
	}
	dtos := make([]DepositRecordPublicDTO, len(records))
	for i, r := range records {
		dtos[i] = FromDepositRecordPublic(r)
	}
	return dtos
}
