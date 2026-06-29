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

	// 构造 migrate 實例
	dsnURL := buildMigrateDSN(cfg)
	m, err := migrate.NewWithSourceInstance("iofs", src, dsnURL.String())
	if err != nil {
		// %w 会把 dsn 含密码一起写到 log；用 redactedDSN 取代
		return fmt.Errorf("migrate new (dsn=%s): %w", redactedDSN(dsnURL), err)
	}

	// 延迟關閉：记录任何 cleanup 錯誤但不中断執行
	defer closeMigrate(m)

	// 執行 pending migrations
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}

// DropAll 刪除資料庫中所有 migration 建立的物件（table、自訂 ENUM type、function、
// trigger、index 與 schema_migrations 版本表），使後續 RunMigrations 能從乾淨狀態
// 重建整個 schema。
//
// 實作以 `DROP SCHEMA public CASCADE; CREATE SCHEMA public` 重建整個 public schema，
// 而非 golang-migrate 的 m.Drop()——後者只刪 table，會殘留自訂 ENUM type，導致重複
// reset 的第二輪在 CREATE TYPE 撞 "already exists"（CI 每次部署 SEED_RESET 會踩到）。
//
// 僅供 seed 的「先 drop 全部表、再重新倒入假資料」流程使用（dev / staging）。
// 此操作具破壞性會清空所有資料；正式環境嚴禁呼叫——呼叫端（cmd/seed）已以
// APP_ENV=prod 中止把關，本函式不額外判斷。
func DropAll(cfg config.DatabaseConfig) error {
	db, err := Connect(cfg)
	if err != nil {
		return fmt.Errorf("connect for drop: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("acquire sql db for drop: %w", err)
	}
	defer sqlDB.Close()

	// CASCADE 一併清掉 schema 內的所有物件（含 enum type / function / trigger）。
	// 重建 public schema 後，連線使用者（dev/staging 的 DB owner）即為其 owner。
	if _, err := sqlDB.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		return fmt.Errorf("reset public schema: %w", err)
	}

	return nil
}

// buildMigrateDSN 以 url.URL 构造 migrate 連線 DSN，确保密码特殊字符被正确 escape。
func buildMigrateDSN(cfg config.DatabaseConfig) *url.URL {
	return &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.Name,
		RawQuery: url.Values{
			"sslmode":           {cfg.SSLMode},
			"statement_timeout": {strconv.Itoa(int(migrationStatementTimeout.Milliseconds()))},
		}.Encode(),
	}
}

// closeMigrate 關閉 migrate 實例，记录任何 cleanup 錯誤但不中断執行。
func closeMigrate(m *migrate.Migrate) {
	srcErr, dbErr := m.Close()
	if srcErr != nil {
		logger.L().Warn("migrate source close error", zap.Error(srcErr))
	}
	if dbErr != nil {
		logger.L().Warn("migrate db close error", zap.Error(dbErr))
	}
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
