package audit

// EventType 审计事件类型
type EventType string

const (
	EventRegisterSuccess    EventType = "register_success"
	EventRegisterFailed     EventType = "register_failed"
	EventLoginSuccess       EventType = "login_success"
	EventLoginFailed        EventType = "login_failed"
	EventRefreshRotated     EventType = "refresh_rotated"
	EventRefreshGraceHit    EventType = "refresh_grace_hit"
	EventReplayDetected     EventType = "replay_detected"
	EventLogoutSuccess      EventType = "logout_success"
	EventRevokeSessionOther EventType = "revoke_session_other"
	EventRevokeAllSessions  EventType = "revoke_all_sessions"
	EventBlacklistHit       EventType = "blacklist_hit"
)

// AuditEvent 审计事件结构
type AuditEvent struct {
	EventType  EventType              `json:"event_type"`
	Timestamp  int64                  `json:"timestamp"` // unix 秒
	UserID     string                 `json:"user_id"`
	Username   string                 `json:"username"`
	UserType   string                 `json:"user_type"`   // cms / member
	ClientID   string                 `json:"client_id"`
	FamilyID   string                 `json:"family_id"`
	IPAddress  string                 `json:"ip_address"`
	Result     string                 `json:"result"`      // success / failure reason
	Details    map[string]interface{} `json:"details"`
	RequestID  string                 `json:"request_id"`
}
