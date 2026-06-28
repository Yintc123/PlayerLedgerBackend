package repository

import (
	"context"

	"gorm.io/gorm"
)

// Transactor 抽象 DB transaction 控制（cms-users-api §10.1）。
//
// Service 層 INV-1 / INV-3 等需要「先讀 → 判斷 → 寫」原子操作的不變量都必須包在 WithTx 內：
//
//	err := tx.WithTx(ctx, func(ctx context.Context) error {
//	    count, _ := repo.CountActiveAdmins(ctx)
//	    if count <= 1 && targetIsAdmin { return apperr.ErrLastAdminLockout }
//	    return repo.Update(ctx, id, patch)
//	})
//
// Repository 層用 dbFromCtx 取得 tx-scoped *gorm.DB；非 tx 場景下行為與舊版相同。
//
// 巢狀 WithTx 由 *gorm.DB.Transaction 內建支援，會自動切換為 SAVEPOINT semantics。
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// txCtxKey 用 unexported 型別作 context key，避免跨 package 撞 key。
type txCtxKey struct{}

// gormTransactor 為 Transactor 的預設 GORM 實作。
type gormTransactor struct {
	db *gorm.DB
}

// NewTransactor 由 *gorm.DB 建構預設實作。
func NewTransactor(db *gorm.DB) Transactor {
	return &gormTransactor{db: db}
}

// WithTx 在 GORM transaction 內呼叫 fn；fn 回 nil 自動 commit，回 err 自動 rollback。
// panic 同樣會 rollback（由 gorm.Transaction 內建處理）並向外重新 panic。
func (t *gormTransactor) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	// 已在 tx 內 → 直接重用，不開新 SAVEPOINT。
	// 重用語意：若內層 fn 回 err，外層 fn 收到 err 並決定是否 rollback；
	// 與「巢狀 SAVEPOINT」差別在無法 partial rollback，但 cms-users-api 場景不需要。
	if existing, ok := ctx.Value(txCtxKey{}).(*gorm.DB); ok && existing != nil {
		return fn(ctx)
	}

	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, txCtxKey{}, tx)
		return fn(txCtx)
	})
}

// hasTx 回報 ctx 是否掛有進行中的 transaction（供 repo 決定是否加 FOR UPDATE 行鎖，§10.2）。
func hasTx(ctx context.Context) bool {
	tx, ok := ctx.Value(txCtxKey{}).(*gorm.DB)
	return ok && tx != nil
}

// dbFromCtx 取 ctx 內的 tx-scoped *gorm.DB；若 ctx 沒掛 tx 就回 fallback（通常為 r.db）。
// 所有 repo method 應透過此 helper 取連線，才能無痛切換 tx / 非 tx 場景。
func dbFromCtx(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txCtxKey{}).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return fallback
}
