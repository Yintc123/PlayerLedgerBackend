// Command seed 將開發用假資料寫入資料庫：20 筆 members + 50 筆 deposit_records。
//
// 設計原則（比照 internal/service.EnsureAdminFromConfig）：
//   - seed 走應用層、不寫進版控的 migration SQL，避免把假資料/密碼帶進正式部署流程。
//   - 嚴禁在 prod 執行（APP_ENV=prod 直接中止）。
//   - 冪等：members 以 username upsert、deposits 以 reference_no 唯一索引去重，可重複執行。
//
// 使用：載入 .env 後執行
//
//	APP_ENV=dev go run ./cmd/seed
//
// 環境變數 SEED_RESET=true/1：先 drop 全部表再重建 schema 並重新倒入（破壞性，dev/staging 限定，
// prod 仍由 APP_ENV 把關拒絕）。CI/CD 以此達成「drop 後重新倒入」。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/yintengching/playerledger/config"
	"github.com/yintengching/playerledger/internal/apperr"
	"github.com/yintengching/playerledger/internal/model"
	"github.com/yintengching/playerledger/internal/repository"
	"github.com/yintengching/playerledger/internal/service"
	"github.com/yintengching/playerledger/pkg/auth/hasher"
	"github.com/yintengching/playerledger/pkg/database"
	"github.com/yintengching/playerledger/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := logger.Init(cfg.Log, cfg.App.Env); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	log := logger.L()

	// 安全閘：seed 僅供 dev/staging，正式環境一律拒絕。
	if cfg.App.Env == "prod" {
		log.Fatal("refusing to seed: APP_ENV=prod（假資料禁止進入正式環境）")
	}

	db, err := database.Connect(cfg.Database)
	if err != nil {
		log.Fatal("failed to connect database", zap.Error(err))
	}

	// SEED_RESET=true/1：先 drop 全部表，再由下方 migrations 重建 schema、重新倒入假資料。
	// 具破壞性（清空整庫），僅供 dev / staging；prod 已於上方 APP_ENV 把關中止。
	// 供 CI/CD「drop 後重新倒入」流程使用（見 .github/workflows/ci.yml 的 seed-db job）。
	if reset := os.Getenv("SEED_RESET"); reset == "true" || reset == "1" {
		log.Warn("SEED_RESET enabled: dropping ALL tables before reseeding",
			zap.String("env", cfg.App.Env))
		if err := database.DropAll(cfg.Database); err != nil {
			log.Fatal("failed to drop database before reseed", zap.Error(err))
		}
		log.Info("all tables dropped; recreating schema via migrations")
	}

	// 確保 schema 已存在（fresh DB / reset 後友善）。
	if err := database.RunMigrations(cfg.Database); err != nil {
		log.Fatal("failed to run migrations", zap.Error(err))
	}

	ctx := context.Background()

	// ── 1. 共用 bcrypt password hash ──
	h := hasher.NewBcryptHasher(cfg.JWT.BcryptCost)
	passwordHash, err := h.Hash(seedPassword)
	if err != nil {
		log.Fatal("failed to hash seed password", zap.Error(err))
	}

	// ── 2. 確保 admin 存在（reset 會清掉 server 建立的 admin；此處冪等重建，
	//        讓 deposit 有 operator 可用，並使資料庫 reset 後即可登入）──
	cmsUserRepo := repository.NewCMSUserRepository(db)
	if _, err := service.EnsureAdminFromConfig(ctx, cmsUserRepo, h, cfg.Admin.Username, cfg.Admin.Password); err != nil {
		log.Fatal("failed to ensure admin", zap.Error(err))
	}

	// ── 3. Upsert 20 筆 members（取回真實 ID 供 deposit FK 使用）──
	members, createdMembers, err := upsertMembers(ctx, db, buildMembers(memberCount, passwordHash))
	if err != nil {
		log.Fatal("failed to seed members", zap.Error(err))
	}
	log.Info("members seeded",
		zap.Int("created", createdMembers),
		zap.Int("total", len(members)),
	)

	// ── 4. operator 取現有 admin（若有），否則 deposit 的 operator 留空 ──
	operatorID := lookupAdminOperator(ctx, log, cmsUserRepo, cfg.Admin.Username)

	// ── 5. 寫入 50 筆 deposit_records（reference_no 衝突即視為已存在而略過）──
	depositRepo := repository.NewDepositRecordRepository(db)
	deposits := buildDeposits(depositCount, members, operatorID)
	inserted, skipped, err := insertDeposits(ctx, db, depositRepo, deposits)
	if err != nil {
		log.Fatal("failed to seed deposit records", zap.Error(err))
	}
	log.Info("deposit records seeded",
		zap.Int("inserted", inserted),
		zap.Int("skipped_existing", skipped),
		zap.Int("total", len(deposits)),
	)

	log.Info("seed completed",
		zap.String("env", cfg.App.Env),
		zap.String("seed_player_password", seedPassword),
	)
}

// upsertMembers 以 username 冪等建立 members。
// MemberRepository 不提供 Create（會員註冊未開放），故 seed 直接走 GORM。
// 回傳每筆對應的持久化 member（含真實 ID）、本次新建數量。
func upsertMembers(ctx context.Context, db *gorm.DB, desired []model.Member) ([]model.Member, int, error) {
	out := make([]model.Member, 0, len(desired))
	created := 0
	for _, m := range desired {
		// 用 Find（非 First）查存在性：查無回 RowsAffected=0 而非 ErrRecordNotFound，
		// 避免 GORM logger 把「正常的不存在」當 ERROR 印出。
		var existing model.Member
		res := db.WithContext(ctx).
			Where("username = ? AND deleted_at IS NULL", m.Username).
			Limit(1).Find(&existing)
		if res.Error != nil {
			return nil, created, fmt.Errorf("lookup member %s: %w", m.Username, res.Error)
		}
		if res.RowsAffected > 0 {
			out = append(out, existing)
			continue
		}
		if err := db.WithContext(ctx).Create(&m).Error; err != nil {
			return nil, created, fmt.Errorf("create member %s: %w", m.Username, err)
		}
		created++
		out = append(out, m)
	}
	return out, created, nil
}

// lookupAdminOperator 找出 admin 帳號當作 deposit operator；找不到則回 nil（operator 為 nullable FK）。
func lookupAdminOperator(ctx context.Context, log *zap.Logger, repo repository.CMSUserRepository, username string) *uuid.UUID {
	if username == "" {
		return nil
	}
	admin, err := repo.FindByUsername(ctx, username)
	if err != nil {
		if !errors.Is(err, apperr.ErrNotFound) {
			log.Warn("lookup admin operator failed, leaving operator empty", zap.Error(err))
		} else {
			log.Warn("admin not found, deposits will have no operator", zap.String("username", username))
		}
		return nil
	}
	id := admin.ID
	return &id
}

// insertDeposits 冪等寫入：先查出已存在的 reference_no，只插入缺少的，
// 避免 re-run 時撞唯一索引、讓 GORM logger 印出整批 duplicate-key ERROR。
func insertDeposits(ctx context.Context, db *gorm.DB, repo repository.DepositRecordRepository, recs []model.DepositRecord) (inserted, skipped int, err error) {
	var existingRefs []string
	if e := db.WithContext(ctx).Model(&model.DepositRecord{}).
		Where("reference_no LIKE ?", seedRefPrefix+"%").
		Pluck("reference_no", &existingRefs).Error; e != nil {
		return 0, 0, fmt.Errorf("list existing seed deposits: %w", e)
	}
	exists := make(map[string]bool, len(existingRefs))
	for _, r := range existingRefs {
		exists[r] = true
	}

	for i := range recs {
		if recs[i].ReferenceNo != nil && exists[*recs[i].ReferenceNo] {
			skipped++
			continue
		}
		if e := repo.Create(ctx, &recs[i]); e != nil {
			return inserted, skipped, fmt.Errorf("create deposit %v: %w", recs[i].ReferenceNo, e)
		}
		inserted++
	}
	return inserted, skipped, nil
}
