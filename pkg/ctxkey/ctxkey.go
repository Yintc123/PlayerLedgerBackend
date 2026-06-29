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

// Actor 代表發起本次請求的操作者（CMS staff）；供 service 寫 audit log 時取用，
// 讓 service 介面維持純 context.Context 而不需在每個方法簽章夾帶 operator 參數。
type Actor struct {
	UserID string
	Role   string
}

type actorKey struct{}

// SetActor 注入操作者身分到 ctx；由 handler 在呼叫 service 前以 token claims 設定。
func SetActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom 从 ctx 取操作者身分；未注入時回零值 Actor。
func ActorFrom(ctx context.Context) Actor {
	if ctx == nil {
		return Actor{}
	}
	a, _ := ctx.Value(actorKey{}).(Actor)
	return a
}
