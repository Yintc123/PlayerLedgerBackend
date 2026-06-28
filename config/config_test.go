package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_WithEnvVars(t *testing.T) {
	// 清空環境变量后设置最小必需值
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "dev", cfg.App.Env)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, "localhost", cfg.Redis.Host)
}

func TestLoad_ValidateAllowCredentialsWithWildcardOrigin(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "*")
	t.Setenv("ALLOW_CREDENTIALS", "true")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ALLOW_CREDENTIALS=true")
}

func TestLoad_ProdRequiresReleaseMode(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("DB_SSLMODE", "require")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GIN_MODE=release")
}

func TestLoad_ProdRequiresSSLMode(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "release")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("DB_SSLMODE", "disable")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_SSLMODE=disable")
}

func TestLoad_RateLimitValidation(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_IP_PERIOD", "1s")
	// 缺少 RATE_LIMIT_IP_MAX

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RATE_LIMIT_IP_MAX")
}

func TestLoad_DefaultValues(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 5432, cfg.Database.Port)
	assert.Equal(t, "disable", cfg.Database.SSLMode)
	assert.Equal(t, 25, cfg.Database.MaxOpenConns)
	assert.Equal(t, 6379, cfg.Redis.Port)
	assert.Equal(t, time.Duration(15*time.Minute), cfg.JWT.AccessTTL)
	assert.Equal(t, "playerledger", cfg.JWT.Issuer)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "/metrics", cfg.Metrics.Path)
}

func TestLoad_ClientPoliciesDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	cfg, err := Load()
	require.NoError(t, err)

	assert.NotNil(t, cfg.JWT.ClientPolicies)
	assert.Contains(t, cfg.JWT.ClientPolicies, "cms-web")
	assert.Equal(t, time.Duration(1*time.Hour), cfg.JWT.ClientPolicies["cms-web"].RefreshTTL)
	assert.Equal(t, time.Duration(8*time.Hour), cfg.JWT.ClientPolicies["cms-web"].AbsoluteTTL)
	assert.Contains(t, cfg.JWT.ClientPolicies, "ios-app")
	assert.Equal(t, time.Duration(720*time.Hour), cfg.JWT.ClientPolicies["ios-app"].RefreshTTL)
}

func TestLoad_MissingRequiredField(t *testing.T) {
	// 不设置 PORT，使用 0 将导致驗證失败
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "0")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "super-secret-key-32-bytes-long!!")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate config")
}

func TestConfig_Validate_WeakJWTSecret(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "8080")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "postgres")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_NAME", "playerledger")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("JWT_SECRET", "short")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-key-32-bytes-long!!")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate config")
}
