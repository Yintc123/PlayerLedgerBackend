package hasher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestBcryptHasher_Hash_Success(t *testing.T) {
	h := NewBcryptHasher(12)
	hash, err := h.Hash("MySecurePassword123")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestBcryptHasher_Hash_DifferentHashes(t *testing.T) {
	h := NewBcryptHasher(12)
	hash1, _ := h.Hash("MySecurePassword123")
	hash2, _ := h.Hash("MySecurePassword123")
	assert.NotEqual(t, hash1, hash2, "bcrypt 使用隨機 salt，同密碼雜湊不同")
}

func TestBcryptHasher_Hash_EmptyPassword(t *testing.T) {
	h := NewBcryptHasher(12)
	hash, err := h.Hash("")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestBcryptHasher_Hash_LongPassword(t *testing.T) {
	h := NewBcryptHasher(12)
	password := string(make([]byte, 71)) // bcrypt 72 byte 限制
	for i := range []byte(password) {
		[]byte(password)[i] = 'a'
	}
	hash, err := h.Hash(password)
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestBcryptHasher_Hash_ValidCostRange(t *testing.T) {
	for cost := 10; cost <= 15; cost++ {
		h := NewBcryptHasher(cost)
		hash, err := h.Hash("TestPassword123")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	}
}

func TestBcryptHasher_Compare_Matching(t *testing.T) {
	h := NewBcryptHasher(12)
	hash, err := h.Hash("MySecurePassword123")
	require.NoError(t, err)

	err = h.Compare(hash, "MySecurePassword123")
	assert.NoError(t, err)
}

func TestBcryptHasher_Compare_Mismatch(t *testing.T) {
	h := NewBcryptHasher(12)
	hash, err := h.Hash("MySecurePassword123")
	require.NoError(t, err)

	err = h.Compare(hash, "WrongPassword456")
	assert.ErrorIs(t, err, ErrMismatch)
}

func TestBcryptHasher_Compare_InvalidHash(t *testing.T) {
	h := NewBcryptHasher(12)
	err := h.Compare("not-a-valid-bcrypt-hash", "MyPassword")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrMismatch)
}

func TestBcryptHasher_Compare_EmptyHash(t *testing.T) {
	h := NewBcryptHasher(12)
	err := h.Compare("", "MyPassword")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrMismatch)
}

func TestBcryptHasher_Compare_CaseSensitive(t *testing.T) {
	h := NewBcryptHasher(12)
	hash, err := h.Hash("MyPassword")
	require.NoError(t, err)

	err = h.Compare(hash, "mypassword")
	assert.ErrorIs(t, err, ErrMismatch)
}

func TestBcryptHasher_Compare_WithSpecialChars(t *testing.T) {
	h := NewBcryptHasher(12)
	password := "P@$$w0rd!#%&*"
	hash, err := h.Hash(password)
	require.NoError(t, err)

	err = h.Compare(hash, password)
	assert.NoError(t, err)
}

func TestBcryptHasher_Compare_WithUnicode(t *testing.T) {
	h := NewBcryptHasher(12)
	password := "密碼123🔐"
	hash, err := h.Hash(password)
	require.NoError(t, err)

	err = h.Compare(hash, password)
	assert.NoError(t, err)
}

func TestBcryptHasher_Compare_CostIndependent(t *testing.T) {
	h1 := NewBcryptHasher(12)
	hash, err := h1.Hash("TestPassword")
	require.NoError(t, err)

	h2 := NewBcryptHasher(13)
	err = h2.Compare(hash, "TestPassword")
	assert.NoError(t, err)
}

func TestBcryptHasher_RealBcryptInterop(t *testing.T) {
	password := "InteropTest"
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	require.NoError(t, err)

	h := NewBcryptHasher(12)
	err = h.Compare(string(bcryptHash), password)
	assert.NoError(t, err)
}

func TestPolicyOf_ReturnsInputCost(t *testing.T) {
	for _, cost := range []int{10, 11, 12, 13, 14, 15} {
		assert.Equal(t, cost, PolicyOf(cost))
	}
}

func TestErrMismatch_Sentinel(t *testing.T) {
	h := NewBcryptHasher(12)
	hash, err := h.Hash("Test")
	require.NoError(t, err)

	err = h.Compare(hash, "Wrong")
	assert.ErrorIs(t, err, ErrMismatch)
}

func TestBcryptHasher_NewBcryptHasher_ReturnsHasher(t *testing.T) {
	h := NewBcryptHasher(12)
	assert.NotNil(t, h)
	// NewBcryptHasher 簽名已宣告回傳 Hasher，編譯期即強制；無需 var _ Hasher = h 冗餘斷言。
}

func TestBcryptHasher_WhitespacePassword(t *testing.T) {
	h := NewBcryptHasher(12)
	password := "   "
	hash, err := h.Hash(password)
	require.NoError(t, err)

	err = h.Compare(hash, password)
	assert.NoError(t, err)
}

func TestBcryptHasher_Compare_NewlinePassword(t *testing.T) {
	h := NewBcryptHasher(12)
	password := "Pass\nword\nTest"
	hash, err := h.Hash(password)
	require.NoError(t, err)

	err = h.Compare(hash, password)
	assert.NoError(t, err)
}

func TestBcryptHasher_Compare_BinaryPassword(t *testing.T) {
	h := NewBcryptHasher(12)
	password := "Pass\x00word"
	hash, err := h.Hash(password)
	require.NoError(t, err)

	err = h.Compare(hash, password)
	assert.NoError(t, err)
}

func TestBcryptHasher_Hash_WithMinimumCost(t *testing.T) {
	h := NewBcryptHasher(10)
	hash, err := h.Hash("TestPassword123")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestBcryptHasher_Hash_WithMaximumCost(t *testing.T) {
	h := NewBcryptHasher(15)
	hash, err := h.Hash("TestPassword123")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}
