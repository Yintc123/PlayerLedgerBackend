package model

// CMSUser CMS 内部用户（员工）
type CMSUser struct {
	Base
	Username     string `gorm:"size:64;not null;uniqueIndex:uq_cms_users_username,where:deleted_at IS NULL"`
	PasswordHash string `gorm:"size:72;not null"`
	Role         string `gorm:"size:16;not null"` // admin / user / viewer
}

func (CMSUser) TableName() string {
	return "cms_users"
}
