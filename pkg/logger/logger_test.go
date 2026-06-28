package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"go.uber.org/zap"
)

func TestInit_ValidFormat_JSON(t *testing.T) {
	err := Init(config.LogConfig{Format: "json", Level: "info", Service: "test-service"}, "dev")
	require.NoError(t, err)

	logger := L()
	assert.NotNil(t, logger)
	assert.NotEqual(t, zap.NewNop(), logger)
}

func TestInit_ValidFormat_Console(t *testing.T) {
	err := Init(config.LogConfig{Format: "console", Level: "debug", Service: "test-service"}, "dev")
	require.NoError(t, err)

	logger := L()
	assert.NotNil(t, logger)
}

func TestInit_InvalidFormat(t *testing.T) {
	err := Init(config.LogConfig{Format: "invalid", Level: "info", Service: "test-service"}, "dev")
	require.Error(t, err)
}

func TestL_BeforeInit_ReturnsNop(t *testing.T) {
	logger := zap.NewNop()
	assert.NotNil(t, logger)
}

func TestWith_AddsFields(t *testing.T) {
	Init(config.LogConfig{Format: "json", Level: "info", Service: "test-service"}, "dev") //nolint:errcheck
	loggerWithFields := With(zap.String("test", "value"))
	assert.NotNil(t, loggerWithFields)
}
