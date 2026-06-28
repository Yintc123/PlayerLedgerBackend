package ctxkey

import "context"

const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// SetRequestID 注入 request_id 到 ctx；由 RequestID middleware 在 request 入口呼叫。
func SetRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID 从 ctx 取 request_id；未注入（例如 background goroutine）回空字串。
// 给下游纯 context.Context 介面用（service / audit logger / repository）。
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(requestIDKey{}).(string)
	return s
}
