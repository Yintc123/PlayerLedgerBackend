package ua

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDeviceLabel_Chrome(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	label := ParseDeviceLabel(ua)
	assert.NotEmpty(t, label)
	assert.NotEqual(t, label, "Unknown")
}

func TestParseDeviceLabel_Safari(t *testing.T) {
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15"
	label := ParseDeviceLabel(ua)
	assert.NotEmpty(t, label)
	assert.NotEqual(t, label, "Unknown")
}

func TestParseDeviceLabel_Empty(t *testing.T) {
	label := ParseDeviceLabel("")
	assert.Equal(t, label, "Unknown")
}

func TestParseDeviceLabel_Invalid(t *testing.T) {
	label := ParseDeviceLabel("xxx")
	assert.NotEmpty(t, label)
}
