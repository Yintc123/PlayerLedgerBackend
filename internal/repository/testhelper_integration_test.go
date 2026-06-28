//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/pkg/database"
	"github.com/yintengching/playerledger/pkg/logger"
	"gorm.io/gorm"
)

var (
	testDB        *gorm.DB
	testContainer testcontainers.Container
)

// TestMain bootstraps the integration test environment.
// Mode 1 — TestContainers (CI default): 設定 USE_TESTCONTAINERS=1 自動拉一個 ephemeral
// postgres:16-alpine 容器，跑 migrations，測試結束自動拆除。
// Mode 2 — Local docker-compose (本地手動): docker compose -f docker-compose.test.yml up -d，
// 不設 USE_TESTCONTAINERS，直接連 localhost:5433。
func TestMain(m *testing.M) {
	if err := logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev"); err != nil {
		fmt.Fprintf(os.Stderr, "logger init: %v\n", err)
		os.Exit(1)
	}

	cfg, cleanup, err := setupTestDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup test db: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	testDB, err = database.Connect(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect test db: %v\n", err)
		os.Exit(1)
	}

	if err := database.RunMigrations(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	os.Exit(code)
}

func setupTestDB() (config.DatabaseConfig, func(), error) {
	if os.Getenv("USE_TESTCONTAINERS") == "" {
		// Local docker-compose mode
		return config.DatabaseConfig{
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
		}, func() {}, nil
	}

	// TestContainers mode (CI)
	ctx := context.Background()
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("playerledger_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return config.DatabaseConfig{}, nil, fmt.Errorf("postgres container: %w", err)
	}
	testContainer = pgContainer

	host, err := pgContainer.Host(ctx)
	if err != nil {
		return config.DatabaseConfig{}, nil, fmt.Errorf("container host: %w", err)
	}
	mappedPort, err := pgContainer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return config.DatabaseConfig{}, nil, fmt.Errorf("container port: %w", err)
	}
	port, err := strconv.Atoi(mappedPort.Port())
	if err != nil {
		return config.DatabaseConfig{}, nil, fmt.Errorf("port atoi: %w", err)
	}

	cleanup := func() {
		_ = pgContainer.Terminate(context.Background())
	}

	return config.DatabaseConfig{
		Host:             host,
		Port:             port,
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
	}, cleanup, nil
}

// WithTx 為測試提供 transaction 化的 *gorm.DB。
// 測試結束時自動 ROLLBACK，下一個測試看到乾淨狀態。
func WithTx(t *testing.T) *gorm.DB {
	t.Helper()
	tx := testDB.Begin()
	if tx.Error != nil {
		t.Fatalf("failed to begin transaction: %v", tx.Error)
	}
	t.Cleanup(func() {
		tx.Rollback()
	})
	return tx
}
