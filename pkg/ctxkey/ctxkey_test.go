package ctxkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetRequestID_And_RequestID(t *testing.T) {
	ctx := context.Background()
	id := "test-request-id-123"

	ctx = SetRequestID(ctx, id)
	retrieved := RequestID(ctx)

	assert.Equal(t, id, retrieved)
}

func TestRequestID_NilContext(t *testing.T) {
	var ctx context.Context
	id := RequestID(ctx)
	assert.Equal(t, "", id)
}

func TestRequestID_NoSetRequestID(t *testing.T) {
	ctx := context.Background()
	id := RequestID(ctx)
	assert.Equal(t, "", id)
}

func TestRequestID_TypeMismatch(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDKey{}, 123)
	id := RequestID(ctx)
	assert.Equal(t, "", id)
}

func TestRequestIDHeader_Constant(t *testing.T) {
	assert.Equal(t, "X-Request-ID", RequestIDHeader)
}
