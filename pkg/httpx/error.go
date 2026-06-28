package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/pkg/logger"
)

// ErrorResponse 错误响应格式（避免循环导入，这里重新定义）
type ErrorResponse struct {
	Success   bool                  `json:"success"`
	RequestID string                `json:"request_id"`
	Error     string                `json:"error"`
	Details   []map[string]string   `json:"details,omitempty"`
}

// WriteError 统一的错误响应写入函数
func WriteError(c *gin.Context, statusCode int, errorCode string) {
	requestID := logger.GetRequestID(c)
	resp := ErrorResponse{
		Success:   false,
		RequestID: requestID,
		Error:     errorCode,
	}

	c.JSON(statusCode, resp)
}

// HandleError 根据错误类型生成 HTTP 响应
// 这是一个通用的错误处理函数，可以由 handler 调用
func HandleError(c *gin.Context, err error) {
	requestID := logger.GetRequestID(c)
	statusCode := http.StatusInternalServerError
	errorCode := "internal_server_error"

	// 可在此处添加具体的错误类型处理逻辑
	// 根据规格书 §12.2 / §12.3 进行错误映射

	resp := ErrorResponse{
		Success:   false,
		RequestID: requestID,
		Error:     errorCode,
	}
	c.JSON(statusCode, resp)
}
