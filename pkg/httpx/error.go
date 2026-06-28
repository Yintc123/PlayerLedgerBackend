package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yintengching/playerledger/internal/handler"
	"github.com/yintengching/playerledger/pkg/logger"
)

// WriteError 统一的错误响应写入函数
func WriteError(c *gin.Context, statusCode int, errorCode string, details ...handler.FieldError) {
	requestID := logger.GetRequestID(c)
	resp := handler.ErrorResp(requestID, errorCode, details...)

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

	resp := handler.ErrorResp(requestID, errorCode)
	c.JSON(statusCode, resp)
}
