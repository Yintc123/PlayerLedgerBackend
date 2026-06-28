package database

import (
	"fmt"

	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/pkg/logger"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"moul.io/zapgorm2"
)

// Connect 建立 PostgreSQL 連線并返回 GORM 實例。
//
// 根据规格书 §6.2，DSN 包含 connect_timeout 与 statement_timeout，
// 避免慢查与卡死。連線池参数也由 config 控制。
//
// 錯誤处理：連線失败返回 error，由 main 决定是否 fatal。
func Connect(cfg config.DatabaseConfig) (*gorm.DB, error) {
	dsn := formatDSN(cfg)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		PrepareStmt: cfg.PrepareStmt, // 直连 PG = true；走 PgBouncer transaction mode 必须 false
		Logger:      newGormLogger(), // zapgorm2 包裝全域 zap logger
	})
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return db, nil
}

// formatDSN 根据 DatabaseConfig 组装 PostgreSQL DSN。
//
// 按照规格书 §6.2，DSN 格式为：
// host=... port=... user=... password=... dbname=... sslmode=...
// connect_timeout=... statement_timeout=...
//
// 时间值转换：
// - connect_timeout：秒（PG 原生单位）
// - statement_timeout：毫秒（PG 原生单位）
func formatDSN(cfg config.DatabaseConfig) string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d statement_timeout=%d",
		cfg.Host,
		cfg.Port,
		cfg.User,
		cfg.Password,
		cfg.Name,
		cfg.SSLMode,
		int(cfg.ConnectTimeout.Seconds()),
		int(cfg.StatementTimeout.Milliseconds()),
	)
}

// newGormLogger 整合 zap：将 GORM 内部日志转至全域 zap logger。
//
// 使用 zapgorm2 包裝全域 logger，LogMode 设为 Warn，避免 INFO 级日志
// 污染生产環境日志（verbose query log 仅在调试需要时启用）。
//
// 规格书 §5 / §6.2 要求日志通过 zapgorm2 转至全域 zap。
func newGormLogger() gormlogger.Interface {
	return zapgorm2.New(logger.L()).LogMode(gormlogger.Warn)
}

// Close 關閉数据库連線。
// 在 graceful shutdown 时调用（见 §14.2）。
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	return sqlDB.Close()
}
