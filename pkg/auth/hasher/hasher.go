package hasher

import "errors"

// ErrMismatch 密码不匹配时返回。
var ErrMismatch = errors.New("password mismatch")

// Hasher 定义密码哈希接口。
// 避免直接使用 golang.org/x/crypto/bcrypt，便于未来换 argon2id。
type Hasher interface {
	// Hash 对密码进行哈希，返回哈希值字符串。
	Hash(password string) (string, error)

	// Compare 比较哈希值与明文密码。
	// 密码匹配返回 (true, nil)。
	// 密码不匹配返回 (false, ErrMismatch)。
	// 其他错误返回 (false, err)。
	Compare(hash, password string) (bool, error)
}
