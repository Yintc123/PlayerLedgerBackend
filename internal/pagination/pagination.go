package pagination

import (
	"gorm.io/gorm"
)

// PageRequest 分頁請求（§16）
type PageRequest struct {
	Page     int `form:"page" validate:"omitempty,min=1,max=10000"`
	PageSize int `form:"page_size" validate:"omitempty,min=1,max=100"`
}

// SetDefaults 設定預設值（page=1, page_size=20）
func (p *PageRequest) SetDefaults() {
	if p.Page == 0 {
		p.Page = 1
	}
	if p.PageSize == 0 {
		p.PageSize = 20
	}
}

// Offset 計算 offset
func (p *PageRequest) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Scope 回傳 GORM scope
func (p *PageRequest) Scope() func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Offset(p.Offset()).Limit(p.PageSize)
	}
}

// PageMeta 分頁元資料（§10.2 / §3.5.1 PageMeta schema）
type PageMeta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// CalcPageMeta 計算分頁元資料
func CalcPageMeta(page, pageSize int, total int64) PageMeta {
	return PageMeta{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
}
