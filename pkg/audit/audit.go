package audit

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/yintengching/playerledger/pkg/ctxkey"
	"github.com/yintengching/playerledger/pkg/metrics"
)

// EventType 安全事件類型（§18.3.2）
type EventType string

// gosec G101 false positive: 「token_rotated」是事件代碼，非憑證；本區段內所有值皆為公開 audit event name。
const (
	EventRegisterSuccess EventType = "auth.register_success"
	EventRegisterFailed  EventType = "auth.register_failed"
	EventLoginSuccess    EventType = "auth.login_success"
	EventLoginFailed     EventType = "auth.login_failed"
	EventTokenRotated    EventType = "auth.token_rotated"   // #nosec G101 -- event 名稱非憑證
	EventReplayDetected  EventType = "auth.replay_detected" // ⚠️ 觸發告警
	EventLogout          EventType = "auth.logout"
	EventSessionRevoked  EventType = "auth.session_revoked"
	EventRevokeAll       EventType = "auth.revoke_all"
)

// AuthEvent 安全事件的結構化內容（§18.3.2）。
// Extra 給事件特有欄位用，例如 replay_detected 帶 {presented_jti, current_jti, delta_sec}。
type AuthEvent struct {
	Type      EventType
	UserID    string // actor；未登入事件（如 login_failed）可空
	FamilyID  string // 事件涉及的 family（若有）
	ClientID  string
	IP        string
	UserAgent string
	Extra     map[string]any
}

// Logger audit logger 唯一介面（§18.3.2）。
// Log 不回傳 error：caller 寫 audit 後即視為「已記錄」，不該被 audit sink 寫失敗拖累業務邏輯。
// 實作必須內部 fallback — 主 sink 寫失敗時自動寫 os.Stderr，永不吞錯。
type Logger interface {
	Log(ctx context.Context, event AuthEvent)
	Sync() error
}

// fallbackSyncer 在主 sink 寫失敗時自動 fallback 至次 sink（§18.3.1 永不吞錯）。
// 正常情況只寫主 sink，不雙重寫入。
type fallbackSyncer struct {
	primary  zapcore.WriteSyncer
	fallback zapcore.WriteSyncer
}

func (f *fallbackSyncer) Write(bs []byte) (int, error) {
	n, err := f.primary.Write(bs)
	if err != nil {
		metrics.AuditWriteErrors.Inc()
		_, _ = f.fallback.Write(bs) // best-effort fallback
	}
	return n, err
}

func (f *fallbackSyncer) Sync() error {
	err := f.primary.Sync()
	if err != nil {
		_ = f.fallback.Sync()
	}
	return err
}

// zapAuditLogger 以獨立 zap instance 實作 Logger（§18.3.3）。
type zapAuditLogger struct {
	z *zap.Logger
}

// NewZapLogger 建立獨立 audit logger（§18.3.3）。
//   - path == ""  → 共用 stdout（本機開發預設）
//   - path != ""  → 寫該檔案；主 sink 寫失敗 fallback stderr（§18.3.1）
//     開檔失敗 → 回 error；caller（main）必須 fatal 退出，禁止 fallback 至 stdout。
func NewZapLogger(path string) (Logger, error) {
	core, err := newAuditCore(path)
	if err != nil {
		return nil, fmt.Errorf("audit core: %w", err)
	}
	return &zapAuditLogger{z: zap.New(core)}, nil
}

// newAuditCore 建立固定 JSON encoder + InfoLevel 的 zap core。
func newAuditCore(path string) (zapcore.Core, error) {
	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		MessageKey:     "message",
		LevelKey:       "", // 不輸出 level（audit 永遠 Info）
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	})

	var sink zapcore.WriteSyncer
	if path == "" {
		sink = zapcore.AddSync(os.Stdout)
	} else {
		// gosec G304 false positive: path 來自 LOG_AUDIT_PATH 環境變數，由 ops 在啟動前設定，
		// 非 request 端動態輸入；config.Validate 已驗證 APP_ENV / Path 形式。
		// gosec G302 真實修正: 改用 0o600（audit log 含敏感事件，僅 owner 讀寫）。
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- ops-controlled path
		if err != nil {
			return nil, fmt.Errorf("open audit log file %q: %w", path, err)
		}
		// 主 sink（file）失敗時 fallback 至 stderr（§18.3.1），正常情況不雙重輸出
		sink = &fallbackSyncer{
			primary:  zapcore.AddSync(f),
			fallback: zapcore.AddSync(os.Stderr),
		}
	}

	return zapcore.NewCore(enc, sink, zapcore.InfoLevel), nil
}

// Log 寫入單筆 audit event（§18.3.3）。
// 不手動寫入 timestamp 欄位——encoder 已設 TimeKey: "timestamp" 自動注入；
// 重複 zap.String("timestamp", ...) 會被 encoder 覆蓋或產生未定義行為。
func (l *zapAuditLogger) Log(ctx context.Context, e AuthEvent) {
	l.z.Info(string(e.Type),
		zap.String("event_type", string(e.Type)),
		zap.String("request_id", ctxkey.RequestID(ctx)),
		zap.String("user_id", e.UserID),
		zap.String("fid", e.FamilyID),
		zap.String("client_id", e.ClientID),
		zap.String("ip", e.IP),
		zap.String("user_agent", e.UserAgent),
		zap.Any("extra", e.Extra),
	)
}

// Sync flush 內部 buffer（§18.3.3）。graceful shutdown 必呼叫，且早於 app logger Sync。
func (l *zapAuditLogger) Sync() error {
	return l.z.Sync()
}

// nopLogger 不寫任何東西的 Logger，用於測試或審計初始化失敗時的安全降級。
type nopLogger struct{}

// NewNopLogger 回傳不做任何事的 Logger。
func NewNopLogger() Logger { return &nopLogger{} }

func (n *nopLogger) Log(_ context.Context, _ AuthEvent) {}
func (n *nopLogger) Sync() error                        { return nil }
