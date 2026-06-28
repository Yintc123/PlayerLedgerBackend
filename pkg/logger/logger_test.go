package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInit_ValidFormat_JSON(t *testing.T) {
	err := Init("json", "info", "test-service", "dev")
	require.NoError(t, err)

	logger := L()
	assert.NotNil(t, logger)
	assert.NotEqual(t, zap.NewNop(), logger)
}

func TestInit_ValidFormat_Console(t *testing.T) {
	err := Init("console", "debug", "test-service", "dev")
	require.NoError(t, err)

	logger := L()
	assert.NotNil(t, logger)
}

func TestInit_InvalidFormat(t *testing.T) {
	err := Init("invalid", "info", "test-service", "dev")
	require.Error(t, err)
}

func TestL_BeforeInit_ReturnsNop(t *testing.T) {
	logger := zap.NewNop()
	assert.NotNil(t, logger)
}

func TestWith_AddsFields(t *testing.T) {
	Init("json", "info", "test-service", "dev")
	loggerWithFields := With(zap.String("test", "value"))
	assert.NotNil(t, loggerWithFields)
}
