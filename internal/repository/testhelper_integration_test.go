//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/pkg/database"
	"github.com/yintengching/playerledger/pkg/logger"
	"gorm.io/gorm"
)

var testDB *gorm.DB

func init() {
	// 初始化日誌
	err := logger.Init("json", "info", "test", "dev")
	if err != nil {
		panic(err)
	}

	// 連接測試數據庫
	cfg := config.DatabaseConfig{
		Host:             "localhost",
		Port:             5433,
		User:             "test",
		Password:         "test",
		Name:             "playerledger_test",
		SSLMode:          "disable",
		MaxOpenConns:     5,
		MaxIdleConns:     1,
		ConnMaxLifetime:  5 * time.Minute,
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 10 * time.Second,
		PrepareStmt:      false,
	}

	var connErr error
	testDB, connErr = database.Connect(cfg)
	if connErr != nil {
		panic("failed to connect to test database: " + connErr.Error())
	}

	// 運行 migrations
	err = database.RunMigrations(cfg)
	if err != nil {
		panic("failed to run migrations: " + err.Error())
	}
}

// WithTx 為測試提供 transaction 化的 *gorm.DB。
// 測試結束時自動 ROLLBACK，下一個測試看到乾淨狀態。
func WithTx(t *testing.T) *gorm.DB {
	t.Helper()
	tx := testDB.BeginTx(context.Background(), &gorm.TxOptions{})
	if tx.Error != nil {
		t.Fatalf("failed to begin transaction: %v", tx.Error)
	}
	t.Cleanup(func() {
		tx.Rollback()
	})
	return tx
}
