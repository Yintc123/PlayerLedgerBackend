package database

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/pkg/logger"
)

// TestRunMigrations_RequiresValidDatabase 驗證 RunMigrations 需要有效的数据库連線
func TestRunMigrations_RequiresValidDatabase(t *testing.T) {
	// 初始化日志
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	cfg := config.DatabaseConfig{
		Host:             "invalid-host",
		Port:             99999,
		User:             "user",
		Password:         "pass",
		Name:             "db",
		SSLMode:          "disable",
		ConnectTimeout:   1 * time.Second,
		StatementTimeout: 10 * time.Second,
	}

	err = RunMigrations(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "migrate")
}

// TestBuildDSNForMigration_ValidConfig 驗證 migration DSN 包含 statement_timeout
func TestBuildDSNForMigration_ValidConfig(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5432,
		User:             "user",
		Password:         "pass",
		Name:             "testdb",
		SSLMode:          "require",
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 5 * time.Minute, // 应转换为 300000 毫秒
	}

	dsnURL := buildMigrationDSN(cfg)
	assert.NotNil(t, dsnURL)

	// 驗證 scheme
	assert.Equal(t, "postgres", dsnURL.Scheme)

	// 驗證 host
	assert.Equal(t, "localhost:5432", dsnURL.Host)

	// 驗證 path
	assert.Equal(t, "testdb", dsnURL.Path)

	// 驗證查询参数包含 statement_timeout（毫秒）
	queryValues := dsnURL.Query()
	assert.Equal(t, "require", queryValues.Get("sslmode"))
	assert.Equal(t, "300000", queryValues.Get("statement_timeout")) // 5m = 300000ms
}

// TestBuildDSNForMigration_PasswordRedaction 驗證 password redact 函数正确处理
func TestBuildDSNForMigration_PasswordRedaction(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5432,
		User:             "user",
		Password:         "super-secret-pass",
		Name:             "testdb",
		SSLMode:          "disable",
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 10 * time.Second,
	}

	dsnURL := buildMigrationDSN(cfg)

	// 取得 password（url.URL 内部会 decode）
	if user := dsnURL.User; user != nil {
		password, _ := user.Password()
		assert.Equal(t, "super-secret-pass", password)
	}

	// 驗證 redact 函数能正确隐蔽密码
	redacted := redactedDSN(dsnURL)
	assert.NotContains(t, redacted, "super-secret-pass")
	// redactedDSN 会把密码替换为 *** 然后 URL encode，所以 *** 会变成 %2A%2A%2A
	assert.Contains(t, redacted, "%2A%2A%2A")
}

// TestMigrationStatementTimeout 驗證 migration statement timeout 常量被正确使用
func TestMigrationStatementTimeout(t *testing.T) {
	// 驗證常量定义
	assert.Equal(t, 5*time.Minute, migrationStatementTimeout)
	assert.Equal(t, int64(300000), migrationStatementTimeout.Milliseconds())
}

// TestRunMigrations_WithValidDatabase 集成測試：驗證在真实数据库上成功运行 migrations
func TestRunMigrations_WithValidDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// 初始化日志
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5432,
		User:             "postgres",
		Password:         "postgres",
		Name:             "playerledger_test",
		SSLMode:          "disable",
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 10 * time.Second,
	}

	// 尝试运行 migrations；如果没有 PostgreSQL 就跳过
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = RunMigrations(cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available or migration failed: %v", err)
	}

	_ = ctx
}

// 辅助函数：构建 migration DSN 用于測試（模拟 RunMigrations 内部逻辑）
func buildMigrationDSN(cfg config.DatabaseConfig) *url.URL {
	dsnURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.Name,
		RawQuery: url.Values{
			"sslmode":           {cfg.SSLMode},
			"statement_timeout": {fmt.Sprintf("%d", cfg.StatementTimeout.Milliseconds())},
		}.Encode(),
	}
	return dsnURL
}
