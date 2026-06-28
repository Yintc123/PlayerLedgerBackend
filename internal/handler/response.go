package handler

// Response 统一的成功响应格式
type Response[T any] struct {
	Success   bool        `json:"success"`
	RequestID string      `json:"request_id"`
	Data      T           `json:"data,omitempty"`
	Meta      *PageMeta   `json:"meta,omitempty"`
}

// ErrorResponse 统一的错误响应格式
type ErrorResponse struct {
	Success   bool         `json:"success"`
	RequestID string       `json:"request_id"`
	Error     string       `json:"error"`
	Details   []FieldError `json:"details,omitempty"`
}

// PageMeta 分页元数据
type PageMeta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// FieldError 字段验证错误
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// OK 创建成功响应
func OK[T any](requestID string, data T) *Response[T] {
	return &Response[T]{
		Success:   true,
		RequestID: requestID,
		Data:      data,
	}
}

// OKList 创建列表响应
func OKList[T any](requestID string, data []T, page int, pageSize int, total int64) *Response[[]T] {
	return &Response[[]T]{
		Success:   true,
		RequestID: requestID,
		Data:      data,
		Meta: &PageMeta{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	}
}

// ErrorResp 创建错误响应
func ErrorResp(requestID string, code string, details ...FieldError) *ErrorResponse {
	return &ErrorResponse{
		Success:   false,
		RequestID: requestID,
		Error:     code,
		Details:   details,
	}
}
