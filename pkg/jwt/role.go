package jwt

// UserType 表示用户类型（来自数据源）。
// 区分 CMS 内部人员与一般玩家，透过 utype claim 路由，避免 ID 碰撞。
type UserType string

const (
	UserTypeCMS    UserType = "cms"    // CMS 內部人員
	UserTypeMember UserType = "member" // 一般玩家
)

// IsValid 檢查 UserType 是否有效。
func (u UserType) IsValid() bool {
	switch u {
	case UserTypeCMS, UserTypeMember:
		return true
	}
	return false
}

// Role 表示用户角色。
// CMS 內部人員使用 admin / user / viewer；一般玩家固定使用 member。
type Role string

const (
	// CMS 內部人員
	RoleAdmin  Role = "admin"  // 系統管理員，擁有最高權限
	RoleUser   Role = "user"   // CMS 一般操作人員，基本查詢權限
	RoleViewer Role = "viewer" // CMS 唯讀檢視者，僅能查看特定資料

	// 非 CMS 使用者
	RoleMember Role = "member" // 一般玩家，只能查詢自己的資料
)

// IsValid 檢查 Role 是否有效。
func (r Role) IsValid() bool {
	switch r {
	case RoleAdmin, RoleUser, RoleViewer, RoleMember:
		return true
	}
	return false
}
