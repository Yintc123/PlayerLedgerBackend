package audit

import (
	"bytes"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
