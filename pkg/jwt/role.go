package jwt

// UserType 表示用户类型（来自数据源）
type UserType string

const (
	UserTypeCMS    UserType = "cms"
	UserTypeMember UserType = "member"
)

// Role 表示用户角色（CMS 专用）
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleUser   Role = "user"
	RoleViewer Role = "viewer"
	RoleMember Role = "member" // member 用户固定为 member role
)

// String 返回字符串表示
func (r Role) String() string {
	return string(r)
}

// String 返回字符串表示
func (u UserType) String() string {
	return string(u)
}
