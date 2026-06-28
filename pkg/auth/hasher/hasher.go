package hasher

import "errors"

// ErrMismatch 密碼不匹配時回傳。
var ErrMismatch = errors.New("password mismatch")

// Hasher 定義密碼雜湊介面（§8.3.2）。
// 避免直接使用 golang.org/x/crypto/bcrypt，便於未來換 argon2id。
type Hasher interface {
	// Hash 對密碼進行雜湊，回傳雜湊值字串。
	Hash(password string) (string, error)

	// Compare 比較雜湊值與明文密碼（§8.3.2）。
	// 匹配        → nil
	// 密碼不匹配  → ErrMismatch
	// 其他系統錯誤 → error
	Compare(hash, plain string) error
}

// PolicyOf 從設定中讀取 bcrypt cost 參數。
func PolicyOf(cost int) int {
	return cost
}
