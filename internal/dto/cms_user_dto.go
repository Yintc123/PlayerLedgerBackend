package dto

import (
	"time"

	"github.com/yintengching/playerledger/internal/model"
)

// CMSUserDTO CMS user 對外表示（cms-users-api §5.1）。
// 絕不洩漏 password_hash 或任何 token 欄位；deleted_at 僅在 include_deleted 結果中出現。
// 時間欄位一律以 RFC3339 UTC 字串輸出（對齊 OpenAPI "RFC3339 UTC" 與其他 DTO 慣例）。
type CMSUserDTO struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	Role      string  `json:"role"`       // admin / user / viewer
	CreatedAt string  `json:"created_at"` // RFC3339 UTC
	UpdatedAt string  `json:"updated_at"` // RFC3339 UTC
	DeletedAt *string `json:"deleted_at,omitempty"`
}

// FromCMSUser 從 model 轉換為 DTO。已軟刪除時帶 deleted_at。
func FromCMSUser(u *model.CMSUser) *CMSUserDTO {
	d := &CMSUserDTO{
		ID:        u.ID.String(),
		Username:  u.Username,
		Role:      u.Role,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: u.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if u.DeletedAt.Valid {
		t := u.DeletedAt.Time.UTC().Format(time.RFC3339)
		d.DeletedAt = &t
	}
	return d
}

// FromCMSUserList 批量轉換（保證非 nil，空集合序列化為 []）。
func FromCMSUserList(us []model.CMSUser) []CMSUserDTO {
	dtos := make([]CMSUserDTO, len(us))
	for i := range us {
		dtos[i] = *FromCMSUser(&us[i])
	}
	return dtos
}
