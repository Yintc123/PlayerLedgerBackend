package dto

import (
	"strings"
	"time"

	"github.com/yintengching/playerledger/internal/model"
)

// PlayerDTO 玩家查詢結果（players-api.md §5.1）。
// email / phone 對 viewer 角色遮罩；欄位皆顯式輸出（含 null），不用 omitempty 以維持前端型別穩定。
// 不暴露 username / password_hash。
type PlayerDTO struct {
	PlayerID     string  `json:"player_id"`
	ExternalID   *string `json:"external_id"`
	DisplayName  string  `json:"display_name"`
	Email        *string `json:"email"`
	Phone        *string `json:"phone"`
	Status       string  `json:"status"`
	RegisteredAt string  `json:"registered_at"`  // RFC 3339（= members.created_at）
	LastActiveAt *string `json:"last_active_at"` // 本期恆為 null
}

// PlayerSearchResult 搜尋回應 data（不含 meta，非 page 分頁）。
type PlayerSearchResult struct {
	Players    []PlayerDTO `json:"players"`
	NextCursor *string     `json:"next_cursor"`
}

// FromMember 從 model 轉 PlayerDTO。mask=true 時遮罩 email / phone（viewer 視角）。
// 僅 email / phone 受遮罩；其餘欄位對所有角色皆完整。
func FromMember(m *model.Member, mask bool) PlayerDTO {
	dto := PlayerDTO{
		PlayerID:     m.ID.String(),
		ExternalID:   m.ExternalID,
		DisplayName:  m.DisplayName,
		Status:       string(m.Status),
		RegisteredAt: m.CreatedAt.Format(time.RFC3339),
	}
	if m.Email != nil {
		v := *m.Email
		if mask {
			v = maskEmail(v)
		}
		dto.Email = &v
	}
	if m.Phone != nil {
		v := *m.Phone
		if mask {
			v = maskPhone(v)
		}
		dto.Phone = &v
	}
	if m.LastActiveAt != nil {
		v := m.LastActiveAt.Format(time.RFC3339)
		dto.LastActiveAt = &v
	}
	return dto
}

// FromMemberList 批量轉換（保證非 nil slice，序列化為 [] 而非 null）。
func FromMemberList(members []*model.Member, mask bool) []PlayerDTO {
	dtos := make([]PlayerDTO, 0, len(members))
	for _, m := range members {
		dtos = append(dtos, FromMember(m, mask))
	}
	return dtos
}

// maskEmail 保留本地部分首字 + *** + @domain（players-api.md §5.2）。
// 本地長度 ≤ 1 或無 @ → 整段 ***。
func maskEmail(s string) string {
	at := strings.IndexByte(s, '@')
	if at <= 1 {
		return "***"
	}
	return s[:1] + "***" + s[at:]
}

// maskPhone 保留前 4 碼 + *** + 末 4 碼（players-api.md §5.2）。長度 < 8 → 整段 ***。
func maskPhone(s string) string {
	if len(s) < 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-4:]
}
