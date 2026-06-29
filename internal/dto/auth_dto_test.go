package dto

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/service"
)

func TestFromTokenPair_CopiesFields(t *testing.T) {
	tp := &service.TokenPair{
		AccessToken:      "acc",
		RefreshToken:     "ref",
		TokenType:        "Bearer",
		ExpiresIn:        900,
		RefreshExpiresIn: 86400,
	}
	d := FromTokenPair(tp)
	require.NotNil(t, d)
	assert.Equal(t, "acc", d.AccessToken)
	assert.Equal(t, "ref", d.RefreshToken)
	assert.Equal(t, "Bearer", d.TokenType)
	assert.Equal(t, 900, d.ExpiresIn)
	assert.Equal(t, 86400, d.RefreshExpiresIn)
}

func TestFromTokenPair_Nil_ReturnsNil(t *testing.T) {
	assert.Nil(t, FromTokenPair(nil))
}

func TestFromSessionInfo_CopiesFields(t *testing.T) {
	now := time.Now()
	si := &service.SessionInfo{
		FID:           "f1",
		ClientID:      "c1",
		DeviceLabel:   "iPhone",
		IPAtLogin:     "1.2.3.4",
		CreatedAt:     now,
		LastRotatedAt: now,
		IsCurrent:     true,
	}
	d := FromSessionInfo(si)
	require.NotNil(t, d)
	assert.Equal(t, "f1", d.FID)
	assert.Equal(t, "c1", d.ClientID)
	assert.Equal(t, "iPhone", d.DeviceLabel)
	assert.True(t, d.IsCurrent)
}

func TestFromSessionInfo_Nil_ReturnsNil(t *testing.T) {
	assert.Nil(t, FromSessionInfo(nil))
}

func TestFromSessionInfoList_Empty_NonNil(t *testing.T) {
	got := FromSessionInfoList(nil)
	assert.Equal(t, []SessionInfoDTO{}, got)
}

func TestFromSessionInfoList_PreservesOrder(t *testing.T) {
	sessions := []service.SessionInfo{
		{FID: "f1", IsCurrent: true},
		{FID: "f2", IsCurrent: false},
	}
	got := FromSessionInfoList(sessions)
	require.Len(t, got, 2)
	assert.Equal(t, "f1", got[0].FID)
	assert.True(t, got[0].IsCurrent)
	assert.Equal(t, "f2", got[1].FID)
	assert.False(t, got[1].IsCurrent)
}
