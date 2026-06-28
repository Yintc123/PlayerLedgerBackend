package pagination

import (
	"gorm.io/gorm"
)

// PageRequest 分页请求（§16）
type PageRequest struct {
	Page     int `form:"page" validate:"omitempty,min=1,max=10000"`
	PageSize int `form:"page_size" validate:"omitempty,min=1,max=100"`
}

// SetDefaults 设置默认值（page=1, page_size=20）
func (p *PageRequest) SetDefaults() {
	if p.Page == 0 {
		p.Page = 1
	}
	if p.PageSize == 0 {
		p.PageSize = 20
	}
}

// Offset 计算 offset
func (p *PageRequest) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Scope 返回 GORM scope（用于 query 链式操作）
func (p *PageRequest) Scope() func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Offset(p.Offset()).Limit(p.PageSize)
	}
}

// PageMeta 分页元数据（含在 Response envelope）
type PageMeta struct {
	Page      int   `json:"page"`
	PageSize  int   `json:"page_size"`
	Total     int64 `json:"total"`
	TotalPage int   `json:"total_page"`
}

// CalcPageMeta 计算分页元数据
func CalcPageMeta(page, pageSize int, total int64) PageMeta {
	totalPage := int((total + int64(pageSize) - 1) / int64(pageSize))
	if totalPage < 1 {
		totalPage = 1
	}
	return PageMeta{
		Page:      page,
		PageSize:  pageSize,
		Total:     total,
		TotalPage: totalPage,
	}
}
