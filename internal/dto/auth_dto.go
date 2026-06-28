package dto

import (
	"time"

	"github.com/yintengching/playerledger/internal/service"
)

// TokenPairDTO access + refresh token 对（§17）
type TokenPairDTO struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// FromTokenPair 从 service.TokenPair 转换
func FromTokenPair(tp *service.TokenPair) *TokenPairDTO {
	if tp == nil {
		return nil
	}
	return &TokenPairDTO{
		AccessToken:      tp.AccessToken,
		RefreshToken:     tp.RefreshToken,
		TokenType:        tp.TokenType,
		ExpiresIn:        tp.ExpiresIn,
		RefreshExpiresIn: tp.RefreshExpiresIn,
	}
}

// SessionInfoDTO 会话信息（§17）
type SessionInfoDTO struct {
	FID           string    `json:"fid"`
	ClientID      string    `json:"client_id"`
	DeviceLabel   string    `json:"device_label"`
	IPAtLogin     string    `json:"ip_at_login"`
	CreatedAt     time.Time `json:"created_at"`
	LastRotatedAt time.Time `json:"last_rotated_at"`
	IsCurrent     bool      `json:"is_current"`
}

// FromSessionInfo 从 service.SessionInfo 转换
func FromSessionInfo(si *service.SessionInfo) *SessionInfoDTO {
	if si == nil {
		return nil
	}
	return &SessionInfoDTO{
		FID:           si.FID,
		ClientID:      si.ClientID,
		DeviceLabel:   si.DeviceLabel,
		IPAtLogin:     si.IPAtLogin,
		CreatedAt:     si.CreatedAt,
		LastRotatedAt: si.LastRotatedAt,
		IsCurrent:     si.IsCurrent,
	}
}

// FromSessionInfoList 批量转换，保证返回非 nil slice
func FromSessionInfoList(sessions []service.SessionInfo) []SessionInfoDTO {
	if len(sessions) == 0 {
		return []SessionInfoDTO{}
	}
	dtos := make([]SessionInfoDTO, len(sessions))
	for i := range sessions {
		if dto := FromSessionInfo(&sessions[i]); dto != nil {
			dtos[i] = *dto
		}
	}
	return dtos
}
