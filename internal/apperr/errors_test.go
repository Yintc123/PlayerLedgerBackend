package apperr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppError_Error_WithDetail(t *testing.T) {
	ae := New("INVALID_INPUT", ErrInvalidInput, "amount must be > 0")
	assert.Equal(t, "INVALID_INPUT: amount must be > 0", ae.Error())
}

func TestAppError_Error_WithoutDetail(t *testing.T) {
	ae := New("NOT_FOUND", ErrNotFound, "")
	assert.Equal(t, "NOT_FOUND", ae.Error())
}

func TestAppError_Unwrap_ReturnsWrappedSentinel(t *testing.T) {
	ae := New("CONFLICT", ErrConflict, "dup")
	assert.Equal(t, ErrConflict, ae.Unwrap())
	// errors.Is 應能穿透 AppError 找到 sentinel
	assert.True(t, errors.Is(ae, ErrConflict))
}

func TestAppError_Is_DelegatesToErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrTokenExpired)
	assert.True(t, Is(wrapped, ErrTokenExpired))
	assert.False(t, Is(wrapped, ErrInvalidToken))
}

func TestAppError_Is_NilAndUnrelated(t *testing.T) {
	assert.False(t, Is(nil, ErrNotFound))
	assert.False(t, Is(errors.New("plain"), ErrNotFound))
}

func TestSentinels_AreDistinct(t *testing.T) {
	// 抽樣確認 sentinel 彼此不相等（避免複製貼上同一個 error 值）。
	assert.NotEqual(t, ErrNotFound, ErrUnauthorized)
	assert.NotEqual(t, ErrInvalidTransition, ErrReferenceNoConflict)
	assert.False(t, errors.Is(ErrNotFound, ErrConflict))
}
