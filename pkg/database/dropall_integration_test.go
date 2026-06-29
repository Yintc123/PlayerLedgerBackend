//go:build integration

package database

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/pkg/logger"
)

// startEphemeralPG 拉一個隔離的 postgres:16-alpine 容器供本檔測試使用，回傳連線設定與 cleanup。
func startEphemeralPG(t *testing.T) (config.DatabaseConfig, func()) {
	t.Helper()
	require.NoError(t, logger.Init(config.LogConfig{Format: "json", Level: "info", Service: "test"}, "dev"))

	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx,
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
	require.NoError(t, err)

	host, err := pg.Host(ctx)
	require.NoError(t, err)
	mapped, err := pg.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	port, err := strconv.Atoi(mapped.Port())
	require.NoError(t, err)

	cfg := config.DatabaseConfig{
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
	}
	return cfg, func() { _ = pg.Terminate(context.Background()) }
}

// TestDropAll_AfterMigrate_RemovesEnumTypes 回歸測試：
// DropAll 必須清掉 migration 建立的「自訂 ENUM type」，而非只刪 table。
// 否則重複 reset（CI 每次部署 SEED_RESET）的第二輪會在 CREATE TYPE 撞 "already exists"。
func TestDropAll_AfterMigrate_RemovesEnumTypes(t *testing.T) {
	cfg, cleanup := startEphemeralPG(t)
	defer cleanup()

	require.NoError(t, RunMigrations(cfg), "首次 migrate 應建立 schema 與 enum types")

	require.NoError(t, DropAll(cfg), "DropAll 應成功清空資料庫")

	db, err := Connect(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	var enumCount int
	err = sqlDB.QueryRow(
		`SELECT count(*) FROM pg_type WHERE typname IN ('deposit_status','payment_method','member_status')`,
	).Scan(&enumCount)
	require.NoError(t, err)
	assert.Equal(t, 0, enumCount, "DropAll 後不應殘留任何自訂 enum type")
}

// TestDropAll_IsRepeatable_AllowsReMigrate 回歸測試（端到端）：
// migrate → drop → migrate 必須成功，模擬 CI 連續兩次 SEED_RESET 部署。
func TestDropAll_IsRepeatable_AllowsReMigrate(t *testing.T) {
	cfg, cleanup := startEphemeralPG(t)
	defer cleanup()

	require.NoError(t, RunMigrations(cfg), "第一輪 migrate")
	require.NoError(t, DropAll(cfg), "第一輪 drop")
	require.NoError(t, RunMigrations(cfg), "drop 後重新 migrate 不應因 enum type 殘留而失敗")
	require.NoError(t, DropAll(cfg), "第二輪 drop")
	require.NoError(t, RunMigrations(cfg), "第二輪重新 migrate 仍應成功")
}
