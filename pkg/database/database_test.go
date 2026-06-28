package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/pkg/logger"
	"go.uber.org/zap"
)

// TestConnect_ValidDSN 驗證 Connect 能成功使用有效 DSN 連線
func TestConnect_ValidDSN_Success(t *testing.T) {
	// 初始化日志（必须在 Connect 之前，因为 newGormLogger 依赖全局 logger）
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5432,
		User:             "postgres",
		Password:         "postgres",
		Name:             "playerledger_test",
		SSLMode:          "disable",
		MaxOpenConns:     10,
		MaxIdleConns:     2,
		ConnMaxLifetime:  5 * time.Minute,
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 10 * time.Second,
		PrepareStmt:      true,
	}

	db, err := Connect(cfg)
	if err != nil {
		// 如果測試環境没有 PostgreSQL，跳过測試而不是失败
		t.Skipf("PostgreSQL not available: %v", err)
	}
	require.NotNil(t, db)

	// 驗證 *gorm.DB 不为 nil
	assert.NotNil(t, db)

	// 驗證連線池配置已應用
	sqlDB, err := db.DB()
	require.NoError(t, err)
	assert.NotNil(t, sqlDB)
	assert.Equal(t, cfg.MaxOpenConns, sqlDB.Stats().MaxOpenConnections)

	// 清理
	_ = sqlDB.Close()
}

// TestConnect_InvalidConfig 驗證无效配置返回 error
func TestConnect_InvalidConfig_ReturnsError(t *testing.T) {
	// 初始化日志
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	cfg := config.DatabaseConfig{
		Host:             "invalid-host-xyz",
		Port:             99999, // 无效端口
		User:             "user",
		Password:         "pass",
		Name:             "db",
		SSLMode:          "disable",
		ConnectTimeout:   1 * time.Second,
		StatementTimeout: 10 * time.Second,
	}

	db, err := Connect(cfg)
	assert.Error(t, err)
	assert.Nil(t, db)
}

// TestConnect_DSN_ContainsConnectTimeout 驗證 DSN 包含 connect_timeout
func TestConnect_DSN_ContainsConnectTimeout(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5432,
		User:             "user",
		Password:         "pass",
		Name:             "db",
		SSLMode:          "disable",
		ConnectTimeout:   15 * time.Second,
		StatementTimeout: 20 * time.Second,
	}

	dsn := buildDSN(cfg)
	assert.Contains(t, dsn, "connect_timeout=15")
}

// TestConnect_DSN_ContainsStatementTimeout 驗證 DSN 包含 statement_timeout（毫秒）
func TestConnect_DSN_ContainsStatementTimeout(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5432,
		User:             "user",
		Password:         "pass",
		Name:             "db",
		SSLMode:          "disable",
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 10 * time.Second,
	}

	dsn := buildDSN(cfg)
	assert.Contains(t, dsn, "statement_timeout=10000") // 10s = 10000ms
}

// TestConnect_GormConfig_HasZapLogger 驗證 GORM 配置使用 zapgorm2 logger
func TestConnect_GormConfig_HasZapLogger(t *testing.T) {
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
		PrepareStmt:      true,
	}

	db, err := Connect(cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	require.NotNil(t, db)

	// 驗證 GORM 實例的 logger 存在
	assert.NotNil(t, db.Logger)

	// 清理
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// TestConnect_PrepareStmt_Configuration 驗證 PrepareStmt 配置被正确應用
func TestConnect_PrepareStmt_Configuration(t *testing.T) {
	// 初始化日志
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	tests := []struct {
		name        string
		prepareStmt bool
	}{
		{"with PrepareStmt enabled", true},
		{"with PrepareStmt disabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DatabaseConfig{
				Host:             "localhost",
				Port:             5432,
				User:             "postgres",
				Password:         "postgres",
				Name:             "playerledger_test",
				SSLMode:          "disable",
				PrepareStmt:      tt.prepareStmt,
				ConnectTimeout:   5 * time.Second,
				StatementTimeout: 10 * time.Second,
			}

			db, err := Connect(cfg)
			if err != nil {
				t.Skipf("PostgreSQL not available: %v", err)
			}
			require.NotNil(t, db)

			// 驗證 PrepareStmt 配置被應用到 GORM config
			assert.Equal(t, tt.prepareStmt, db.PrepareStmt)

			// 清理
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		})
	}
}

// TestNewGormLogger_IntegratesZapLogger 驗證 newGormLogger 正确集成 zap logger
func TestNewGormLogger_IntegratesZapLogger(t *testing.T) {
	// 初始化日志
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	gormLogger := newGormLogger()

	// 驗證 logger 不为 nil
	assert.NotNil(t, gormLogger)
}

// TestLogger_Integration 驗證 zap logger 与 GORM 的集成
func TestLogger_Integration_WithZap(t *testing.T) {
	// 初始化日志
	err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev")
	require.NoError(t, err)

	l := logger.L()
	assert.NotNil(t, l)

	// 測試 logger 的基本功能
	l.Info("test log from GORM integration",
		zap.String("module", "database"),
		zap.String("action", "test"),
	)
}

// 辅助函数：从 config 构建 DSN（用于单元測試驗證 DSN 格式）
func buildDSN(cfg config.DatabaseConfig) string {
	return formatDSN(cfg)
}
