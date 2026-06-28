package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/logger"
)

// Response 統一成功回應格式（§10 / §3.5.1 SuccessEnvelope）。
// Data 無 omitempty — 空 slice 序列化為 [] 而非消失。
type Response[T any] struct {
	Success   bool      `json:"success"`
	RequestID string    `json:"request_id"`
	Data      T         `json:"data"`
	Meta      *PageMeta `json:"meta,omitempty"`
}

// PageMeta 分頁元資料（§10.2 / §3.5.1 PageMeta schema）
type PageMeta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// FieldError 欄位驗證錯誤（§3.5.1 FieldError schema）
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// OK 建立成功回應（§10）。
func OK[T any](c *gin.Context, data T) Response[T] {
	return Response[T]{
		Success:   true,
		RequestID: logger.GetRequestID(c),
		Data:      data,
	}
}

// OKList 建立列表回應（§10）。
func OKList[T any](c *gin.Context, data []T, page int, pageSize int, total int64) Response[[]T] {
	return Response[[]T]{
		Success:   true,
		RequestID: logger.GetRequestID(c),
		Data:      data,
		Meta: &PageMeta{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	}
}
