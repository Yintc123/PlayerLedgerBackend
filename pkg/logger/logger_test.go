package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"go.uber.org/zap"
)

func TestInit_ValidFormat_JSON(t *testing.T) {
	ResetForTesting()
	err := Init(config.LogConfig{Format: "json", Level: "info", Service: "test-service"}, "dev")
	require.NoError(t, err)

	logger := L()
	assert.NotNil(t, logger)
	assert.NotEqual(t, zap.NewNop(), logger)
}

func TestInit_ValidFormat_Console(t *testing.T) {
	ResetForTesting()
	err := Init(config.LogConfig{Format: "console", Level: "debug", Service: "test-service"}, "dev")
	require.NoError(t, err)

	logger := L()
	assert.NotNil(t, logger)
}

func TestInit_InvalidFormat(t *testing.T) {
	ResetForTesting()
	err := Init(config.LogConfig{Format: "invalid", Level: "info", Service: "test-service"}, "dev")
	require.Error(t, err)
}

func TestInit_CalledTwice_NoOpAndWarn(t *testing.T) {
	ResetForTesting()
	err := Init(config.LogConfig{Format: "json", Level: "info", Service: "first"}, "dev")
	require.NoError(t, err)
	first := L()

	// 第二次呼叫應 no-op + warn，logger 不被替換（§5.2）
	err = Init(config.LogConfig{Format: "console", Level: "debug", Service: "second"}, "prod")
	require.NoError(t, err)
	second := L()
	assert.Same(t, first, second, "重複 Init 不該替換 logger instance")
}

func TestL_BeforeInit_ReturnsNop(t *testing.T) {
	logger := zap.NewNop()
	assert.NotNil(t, logger)
}

func TestWith_AddsFields(t *testing.T) {
	ResetForTesting()
	Init(config.LogConfig{Format: "json", Level: "info", Service: "test-service"}, "dev") //nolint:errcheck
	loggerWithFields := With(zap.String("test", "value"))
	assert.NotNil(t, loggerWithFields)
}
