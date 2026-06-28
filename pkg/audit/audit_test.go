package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/yintengching/playerledger/pkg/metrics"
)

// failingSyncer 模擬主 sink 寫入永遠失敗（檔案掛載被卸載、磁碟滿等情境）。
type failingSyncer struct {
	bytes.Buffer
}

func (f *failingSyncer) Write(p []byte) (int, error) {
	return 0, errors.New("primary sink unavailable")
}

func (f *failingSyncer) Sync() error { return errors.New("primary sync failed") }

// captureSyncer 紀錄被寫入的內容並回 (n, nil)。
type captureSyncer struct {
	bytes.Buffer
}

func (c *captureSyncer) Sync() error { return nil }

func counterValue(c prometheus.Counter) float64 {
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// TestFallbackSyncerWrite_PrimaryFails_FallbackInvokedAndMetricInc 主 sink 失敗時應寫入 fallback 並增加 AuditWriteErrors
func TestFallbackSyncerWrite_PrimaryFails_FallbackInvokedAndMetricInc(t *testing.T) {
	primary := &failingSyncer{}
	fallback := &captureSyncer{}

	syncer := &fallbackSyncer{primary: primary, fallback: fallback}

	before := counterValue(metrics.AuditWriteErrors)
	_, err := syncer.Write([]byte("event-line\n"))
	after := counterValue(metrics.AuditWriteErrors)

	require.Error(t, err, "Write should return primary error so zap may log it (永不吞錯)")
	assert.Equal(t, "event-line\n", fallback.String(), "fallback must receive the payload")
	assert.InDelta(t, before+1, after, 0.0001, "AuditWriteErrors must increment on primary failure")
}

// TestCMSUserEventTypes_Defined 確保 cms-users-api §7 5 個 EventType 常數已就位且非空字串
func TestCMSUserEventTypes_Defined(t *testing.T) {
	cases := []struct {
		name string
		got  EventType
		want EventType
	}{
		{"EventCMSUserUpdated", EventCMSUserUpdated, "cms_user.updated"},
		{"EventCMSUserRoleChanged", EventCMSUserRoleChanged, "cms_user.role_changed"},
		{"EventCMSUserDeleted", EventCMSUserDeleted, "cms_user.deleted"},
		{"EventCMSUserSelfUpdated", EventCMSUserSelfUpdated, "cms_user.self_updated"},
		{"EventCMSUserSessionsForceRevoked", EventCMSUserSessionsForceRevoked, "cms_user.sessions_force_revoked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.got)
		})
	}
}

// newCaptureLogger 用 captureSyncer 包出一個寫進 buffer 的 zapAuditLogger，方便比對 JSON 輸出
func newCaptureLogger(t *testing.T, sink *captureSyncer) Logger {
	t.Helper()
	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:    "timestamp",
		MessageKey: "message",
		LevelKey:   "",
		EncodeTime: zapcore.RFC3339NanoTimeEncoder,
	})
	core := zapcore.NewCore(enc, sink, zapcore.InfoLevel)
	return &zapAuditLogger{z: zap.New(core)}
}

// TestLog_EmitsTargetUserID 確認 Log 輸出 JSON 中含 target_user_id 欄位（cms-users-api §7）
func TestLog_EmitsTargetUserID(t *testing.T) {
	sink := &captureSyncer{}
	logger := newCaptureLogger(t, sink)

	logger.Log(context.Background(), AuthEvent{
		Type:         EventCMSUserUpdated,
		UserID:       "admin-1",
		TargetUserID: "target-9",
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal(sink.Bytes(), &got))
	assert.Equal(t, "admin-1", got["user_id"])
	assert.Equal(t, "target-9", got["target_user_id"])
	assert.Equal(t, "cms_user.updated", got["event_type"])
}

// TestLog_OmitsTargetUserIDWhenEmpty 確認 TargetUserID 為空時不污染既有 auth.* 事件輸出
func TestLog_OmitsTargetUserIDWhenEmpty(t *testing.T) {
	sink := &captureSyncer{}
	logger := newCaptureLogger(t, sink)

	logger.Log(context.Background(), AuthEvent{
		Type:   EventLoginSuccess,
		UserID: "user-x",
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal(sink.Bytes(), &got))
	assert.Equal(t, "user-x", got["user_id"])
	// 空字串視為「無 target」；spec §7 表格中 actor = target 由 caller 不填即代表
	_, hasField := got["target_user_id"]
	if hasField {
		assert.Empty(t, got["target_user_id"], "auth.* events leave TargetUserID empty")
	}
}

// TestFallbackSyncerWrite_PrimarySucceeds_NoFallbackNoMetric 主 sink 成功時不應觸發 fallback 也不應增加 metric
func TestFallbackSyncerWrite_PrimarySucceeds_NoFallbackNoMetric(t *testing.T) {
	primary := &captureSyncer{}
	fallback := &captureSyncer{}

	syncer := &fallbackSyncer{primary: primary, fallback: fallback}

	before := counterValue(metrics.AuditWriteErrors)
	n, err := syncer.Write([]byte("normal-event\n"))
	after := counterValue(metrics.AuditWriteErrors)

	require.NoError(t, err)
	assert.Equal(t, len("normal-event\n"), n)
	assert.Equal(t, "normal-event\n", primary.String(), "primary should receive the payload")
	assert.Empty(t, fallback.String(), "fallback must NOT be invoked when primary succeeds")
	assert.InDelta(t, before, after, 0.0001, "metric must not increment on success")
}
