package database

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/migrations"
	"github.com/yintengching/playerledger/pkg/logger"
	"go.uber.org/zap"
)

// migrationStatementTimeout 单一 migration statement 的上限。
// 设较长（5 分钟）容纳大型 ALTER / CREATE INDEX；但有限以避免 deadlock 或
// advisory lock 等待把整个 process 卡死，便于監控偵测异常。
const migrationStatementTimeout = 5 * time.Minute

// RunMigrations 執行 embed.FS 内的 migration 脚本。
//
// 根据规格书 §13.3，使用 golang-migrate + embed.FS 构造 migration source，
// 連線到 PostgreSQL 数据库并執行 pending migrations。
//
// 多 instance 同时启动时，golang-migrate 自动透过 PostgreSQL advisory lock
// 序列化執行（见规格书 §13.1 注释）。
//
// 錯誤处理：
// - 构造 migration source 失败 → 回 error
// - 連線数据库失败 → 回 error
// - 執行 migration 失败 → 回 error（除 ErrNoChange 外）
// - 關閉錯誤记 warn 但不回 error（cleanup 顺序问题）
func RunMigrations(cfg config.DatabaseConfig) error {
	// 从 embed.FS 构造 migration source
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migration source: %w", err)
	}

	// 构造 DSN：使用 url.URL 确保密码特殊字符被正确 escape
	dsnURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.Name,
		RawQuery: url.Values{
			"sslmode":           {cfg.SSLMode},
			"statement_timeout": {strconv.Itoa(int(migrationStatementTimeout.Milliseconds()))},
		}.Encode(),
	}

	// 构造 migrate 實例
	m, err := migrate.NewWithSourceInstance("iofs", src, dsnURL.String())
	if err != nil {
		// %w 会把 dsn 含密码一起写到 log；用 redactedDSN 取代
		return fmt.Errorf("migrate new (dsn=%s): %w", redactedDSN(dsnURL), err)
	}

	// 延迟關閉：记录任何 cleanup 錯誤但不中断執行
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			logger.L().Warn("migrate source close error", zap.Error(srcErr))
		}
		if dbErr != nil {
			logger.L().Warn("migrate db close error", zap.Error(dbErr))
		}
	}()

	// 執行 pending migrations
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}

// redactedDSN 把 password 替换成 "***" 后序列化，给 error / log 用。
// 防止日志中泄漏数据库密码。
func redactedDSN(u *url.URL) string {
	redacted := *u
	if u.User != nil {
		redacted.User = url.UserPassword(u.User.Username(), "***")
	}
	return redacted.String()
}
