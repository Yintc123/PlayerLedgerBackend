package hasher

import (
	"golang.org/x/crypto/bcrypt"
)

// BcryptHasher 使用 bcrypt 的哈希实现。
type BcryptHasher struct {
	cost int
}

// NewBcryptHasher 创建 bcrypt hasher，cost 通常为 12。
func NewBcryptHasher(cost int) Hasher {
	return &BcryptHasher{cost: cost}
}

// Hash 实现 Hasher.Hash。
func (h *BcryptHasher) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// Compare 实现 Hasher.Compare。
func (h *BcryptHasher) Compare(hash, password string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return false, ErrMismatch
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
