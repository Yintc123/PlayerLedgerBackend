package hasher

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestBcryptHasher_Hash_Success 测试 Hash 函数成功哈希密码。
func TestBcryptHasher_Hash_Success(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "MySecurePassword123"

	hash, err := hasher.Hash(password)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, password, hash)
}

// TestBcryptHasher_Hash_DifferentHashes 测试同一密码多次哈希得到不同结果（bcrypt 使用随机 salt）。
func TestBcryptHasher_Hash_DifferentHashes(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "MySecurePassword123"

	hash1, err1 := hasher.Hash(password)
	require.NoError(t, err1)

	hash2, err2 := hasher.Hash(password)
	require.NoError(t, err2)

	assert.NotEqual(t, hash1, hash2, "same password should produce different hashes due to random salt")
}

// TestBcryptHasher_Hash_EmptyPassword 测试空密码哈希。
func TestBcryptHasher_Hash_EmptyPassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := ""

	hash, err := hasher.Hash(password)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

// TestBcryptHasher_Hash_LongPassword 测试接近 bcrypt 72 字节限制的密码。
func TestBcryptHasher_Hash_LongPassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	// bcrypt 限制 72 字节，测试接近限制的密码
	passwordBytes := make([]byte, 71)
	for i := range passwordBytes {
		passwordBytes[i] = 'a'
	}
	password := string(passwordBytes)

	hash, err := hasher.Hash(password)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

// TestBcryptHasher_Hash_ValidCostRange 测试各个有效的 cost 值。
func TestBcryptHasher_Hash_ValidCostRange(t *testing.T) {
	password := "TestPassword123"

	for cost := 10; cost <= 15; cost++ {
		t.Run("cost_"+string(rune(cost)), func(t *testing.T) {
			hasher := NewBcryptHasher(cost)
			hash, err := hasher.Hash(password)

			require.NoError(t, err)
			assert.NotEmpty(t, hash)
		})
	}
}

// TestBcryptHasher_Compare_MatchingPassword 测试 Compare 密码匹配返回 true。
func TestBcryptHasher_Compare_MatchingPassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "MySecurePassword123"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestBcryptHasher_Compare_MismatchedPassword 测试 Compare 密码不匹配返回 ErrMismatch。
func TestBcryptHasher_Compare_MismatchedPassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "MySecurePassword123"
	wrongPassword := "WrongPassword456"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, wrongPassword)

	assert.False(t, ok)
	assert.True(t, errors.Is(err, ErrMismatch), "should return ErrMismatch sentinel")
}

// TestBcryptHasher_Compare_InvalidHash 测试 Compare 使用无效哈希值。
func TestBcryptHasher_Compare_InvalidHash(t *testing.T) {
	hasher := NewBcryptHasher(12)
	invalidHash := "not-a-valid-bcrypt-hash"
	password := "MyPassword"

	ok, err := hasher.Compare(invalidHash, password)

	assert.False(t, ok)
	assert.Error(t, err)
	assert.NotEqual(t, err, ErrMismatch)
}

// TestBcryptHasher_Compare_EmptyHash 测试 Compare 使用空哈希值。
func TestBcryptHasher_Compare_EmptyHash(t *testing.T) {
	hasher := NewBcryptHasher(12)
	emptyHash := ""
	password := "MyPassword"

	ok, err := hasher.Compare(emptyHash, password)

	assert.False(t, ok)
	assert.Error(t, err)
	assert.NotEqual(t, err, ErrMismatch)
}

// TestBcryptHasher_Compare_CaseSensitive 测试密码比较区分大小写。
func TestBcryptHasher_Compare_CaseSensitive(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "MyPassword"
	wrongCase := "mypassword"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, wrongCase)

	assert.False(t, ok)
	assert.True(t, errors.Is(err, ErrMismatch))
}

// TestBcryptHasher_Compare_WithSpecialChars 测试包含特殊字符的密码。
func TestBcryptHasher_Compare_WithSpecialChars(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "P@$$w0rd!#%&*"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestBcryptHasher_Compare_WithUnicode 测试包含 Unicode 字符的密码。
func TestBcryptHasher_Compare_WithUnicode(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "密碼123🔐"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestBcryptHasher_Compare_CostIndependent 测试使用不同 cost 的 hasher 仍可验证哈希。
func TestBcryptHasher_Compare_CostIndependent(t *testing.T) {
	// 用 cost=12 创建哈希
	hasher1 := NewBcryptHasher(12)
	password := "TestPassword"

	hash, err := hasher1.Hash(password)
	require.NoError(t, err)

	// 用 cost=13 的 hasher 验证哈希（cost 只影响生成，不影响验证）
	hasher2 := NewBcryptHasher(13)
	ok, err := hasher2.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestBcryptHasher_RealBcryptInterop 测试与 golang.org/x/crypto/bcrypt 的互操作性。
func TestBcryptHasher_RealBcryptInterop(t *testing.T) {
	// 使用原生 bcrypt 生成哈希
	password := "InteropTest"
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	require.NoError(t, err)

	// 用 hasher 验证
	hasher := NewBcryptHasher(12)
	ok, err := hasher.Compare(string(bcryptHash), password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestPolicyOf_ReturnsInputCost 测试 PolicyOf 返回传入的 cost 值。
func TestPolicyOf_ReturnsInputCost(t *testing.T) {
	tests := []int{10, 11, 12, 13, 14, 15}

	for _, cost := range tests {
		t.Run("cost_"+string(rune(cost)), func(t *testing.T) {
			result := PolicyOf(cost)
			assert.Equal(t, cost, result)
		})
	}
}

// TestErrMismatch_Sentinel 测试 ErrMismatch 作为 sentinel error 可被准确识别。
func TestErrMismatch_Sentinel(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "Test"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	_, err = hasher.Compare(hash, "Wrong")

	assert.True(t, errors.Is(err, ErrMismatch))
	assert.Equal(t, ErrMismatch, err)
}

// TestBcryptHasher_NewBcryptHasher_ReturnsHasher 测试 NewBcryptHasher 返回 Hasher 接口。
func TestBcryptHasher_NewBcryptHasher_ReturnsHasher(t *testing.T) {
	hasher := NewBcryptHasher(12)

	assert.NotNil(t, hasher)
	var _ Hasher = hasher
}

// TestBcryptHasher_Compare_PartialHashMatch 测试密码前缀不完全匹配。
func TestBcryptHasher_Compare_PartialHashMatch(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "MyCompletePassword"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	// 尝试用前缀匹配
	ok, err := hasher.Compare(hash, "MyComplete")

	assert.False(t, ok)
	assert.True(t, errors.Is(err, ErrMismatch))
}

// TestBcryptHasher_Hash_WithMinimumCost 测试最小 cost 值。
func TestBcryptHasher_Hash_WithMinimumCost(t *testing.T) {
	hasher := NewBcryptHasher(10)
	password := "TestPassword123"

	hash, err := hasher.Hash(password)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

// TestBcryptHasher_Hash_WithMaximumCost 测试最大 cost 值。
func TestBcryptHasher_Hash_WithMaximumCost(t *testing.T) {
	hasher := NewBcryptHasher(15)
	password := "TestPassword123"

	hash, err := hasher.Hash(password)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

// TestBcryptHasher_WhitespacePassword 测试仅包含空格的密码。
func TestBcryptHasher_WhitespacePassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "   "

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestBcryptHasher_Compare_NewlinePassword 测试包含换行符的密码。
func TestBcryptHasher_Compare_NewlinePassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "Pass\nword\nTest"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestBcryptHasher_Compare_BinaryPassword 测试包含 null 字符的密码。
func TestBcryptHasher_Compare_BinaryPassword(t *testing.T) {
	hasher := NewBcryptHasher(12)
	password := "Pass\x00word"

	hash, err := hasher.Hash(password)
	require.NoError(t, err)

	ok, err := hasher.Compare(hash, password)

	assert.True(t, ok)
	assert.NoError(t, err)
}
