package model

import (
	"time"

	"github.com/google/uuid"
)

type DepositStatus string

const (
	DepositStatusPending   DepositStatus = "pending"
	DepositStatusCompleted DepositStatus = "completed"
	DepositStatusFailed    DepositStatus = "failed"
	DepositStatusCancelled DepositStatus = "cancelled"
	DepositStatusRefunded  DepositStatus = "refunded"
)

type PaymentMethod string

const (
	PaymentMethodBankTransfer     PaymentMethod = "bank_transfer"
	PaymentMethodCreditCard       PaymentMethod = "credit_card"
	PaymentMethodManual           PaymentMethod = "manual"
	PaymentMethodConvenienceStore PaymentMethod = "convenience_store"
	PaymentMethodEWallet          PaymentMethod = "e_wallet"
)

// validStatusTransitions 不可從外部修改；查詢走 CanTransition。
var validStatusTransitions = map[DepositStatus]map[DepositStatus]bool{
	DepositStatusPending:   {DepositStatusCompleted: true, DepositStatusFailed: true, DepositStatusCancelled: true},
	DepositStatusCompleted: {DepositStatusRefunded: true},
	DepositStatusFailed:    {},
	DepositStatusCancelled: {},
	DepositStatusRefunded:  {},
}

// CanTransition 回報從 from 轉到 to 是否合法。
func CanTransition(from, to DepositStatus) bool {
	return validStatusTransitions[from][to]
}

// DepositRecord 儲值紀錄（金融不可刪除，無 deleted_at）。
type DepositRecord struct {
	ID            uuid.UUID     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PlayerID      uuid.UUID     `gorm:"type:uuid;not null"`
	PlayerName    string        `gorm:"type:varchar(64);not null"`
	Amount        int64         `gorm:"type:bigint;not null"`
	Currency      string        `gorm:"type:char(3);not null;default:TWD"`
	Status        DepositStatus `gorm:"type:deposit_status;not null;default:pending"`
	PaymentMethod PaymentMethod `gorm:"type:payment_method;not null"`
	OperatorID    *uuid.UUID    `gorm:"type:uuid"`
	OperatorIP    *string       `gorm:"type:inet"`
	InternalNote  *string       `gorm:"type:text"`
	DisplayNote   *string       `gorm:"type:text"`
	ReferenceNo   *string       `gorm:"type:varchar(128)"`
	CreatedAt     time.Time     `gorm:"not null;autoCreateTime"`
	UpdatedAt     time.Time     `gorm:"not null;autoUpdateTime"`
}

func (DepositRecord) TableName() string {
	return "deposit_records"
}
