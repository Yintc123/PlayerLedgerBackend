package model

// Member 一般玩家（会员）
type Member struct {
	Base
	Username     string `gorm:"size:64;not null;uniqueIndex:uq_members_username,where:deleted_at IS NULL"`
	PasswordHash string `gorm:"size:72;not null"`
}

func (Member) TableName() string {
	return "members"
}
