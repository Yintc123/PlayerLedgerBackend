package hasher

import (
	"golang.org/x/crypto/bcrypt"
)

// BcryptHasher 使用 bcrypt 的雜湊實作。
type BcryptHasher struct {
	cost int
}

// NewBcryptHasher 建立 bcrypt hasher，cost 通常為 12。
func NewBcryptHasher(cost int) Hasher {
	return &BcryptHasher{cost: cost}
}

// Hash 實作 Hasher.Hash。
func (h *BcryptHasher) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// Compare 實作 Hasher.Compare（§8.3.2）。
// 密碼匹配 → nil；不匹配 → ErrMismatch；其他錯誤直接回傳。
func (h *BcryptHasher) Compare(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return ErrMismatch
	}
	return err
}
