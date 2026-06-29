package model

import "time"

// MemberStatus 玩家帳號狀態（players-model.md §2.1）。
type MemberStatus string

const (
	MemberStatusActive MemberStatus = "active"
	MemberStatusFrozen MemberStatus = "frozen"
	MemberStatusClosed MemberStatus = "closed"
)

// Member 一般玩家（会员）。
// v1.0 擴充玩家查詢所需的 profile 欄位（players-model.md §7）。
type Member struct {
	Base
	Username     string `gorm:"size:64;not null;uniqueIndex:uq_members_username,where:deleted_at IS NULL"`
	PasswordHash string `gorm:"size:72;not null"`

	ExternalID   *string      `gorm:"size:64"`
	DisplayName  string       `gorm:"size:64;not null"`
	Email        *string      `gorm:"size:255"`
	Phone        *string      `gorm:"size:32"`
	Status       MemberStatus `gorm:"type:member_status;not null;default:active"`
	LastActiveAt *time.Time
}

func (Member) TableName() string {
	return "members"
}
