package main

import (
	"fmt"
	"math/rand"

	"github.com/google/uuid"
	"github.com/yintengching/playerledger/internal/model"
)

// seed 開發假資料的固定規模與辨識前綴。
// 前綴讓 seed 產物可被辨識、冪等 upsert，也方便日後清理。
const (
	memberCount      = 20
	depositCount     = 50
	seedMemberPrefix = "seed_player_"
	seedRefPrefix    = "SEED-REF-"
	// seedPassword 為所有 seed 玩家共用的固定密碼，僅供 dev/staging 登入測試。
	seedPassword = "Seed-Player-Pw1" // #nosec G101 -- 固定 dev/staging seed 密碼，非正式憑證
	// seedRandSeed 為固定亂數種子：deposit 各欄位值「隨機但有效」，但以固定 seed
	// 產生 → 每次產生 / CI 重跑 / 單元測試之間完全可重現（reproducible）；
	// 各列資料互異（金額、狀態、付款方式、玩家、IP…），又不會每跑一次就漂移。
	seedRandSeed = 20260629
	// seed amount 範圍：100..1,000,000（取百元整數），恆 > 0，符合 DB CHECK (amount > 0)。
	seedMaxAmountUnits = 10000 // 乘以 100 得最大金額
)

// seedStatuses / seedMethods 列出所有合法 enum，供隨機挑選以覆蓋各狀態與付款方式。
var (
	seedStatuses = []model.DepositStatus{
		model.DepositStatusPending,
		model.DepositStatusCompleted,
		model.DepositStatusFailed,
		model.DepositStatusCancelled,
		model.DepositStatusRefunded,
	}
	seedMethods = []model.PaymentMethod{
		model.PaymentMethodBankTransfer,
		model.PaymentMethodCreditCard,
		model.PaymentMethodManual,
		model.PaymentMethodConvenienceStore,
		model.PaymentMethodEWallet,
	}
	// seedOperatorIPs 為合法且非路由的 INET 值（RFC 5737 文件保留段 TEST-NET-1/2/3），
	// 供隨機挑選；不會誤打到真實主機。
	seedOperatorIPs = []string{
		"192.0.2.10", "192.0.2.88", "192.0.2.231",
		"198.51.100.7", "198.51.100.140",
		"203.0.113.10", "203.0.113.199",
	}
	// seedNotes 多樣但合規的備註文字，供隨機挑選。
	seedNotes = []string{
		"臨櫃存款", "ATM 轉帳入金", "信用卡儲值", "超商代收入金",
		"電子錢包儲值", "玩家自助入金", "活動贈點對應入金",
	}
)

// buildMembers 產生 n 筆 deterministic、合規的 seed 玩家。
// 所有玩家共用同一組 bcrypt password hash（dev 方便登入測試）。
// username 形如 seed_player_001，保證唯一且不超過 64 字元上限。
func buildMembers(n int, passwordHash string) []model.Member {
	members := make([]model.Member, 0, n)
	for i := 0; i < n; i++ {
		seq := i + 1
		ext := fmt.Sprintf("EXT-%05d", seq)
		email := fmt.Sprintf("%s%03d@example.com", seedMemberPrefix, seq) // 已小寫，符合 email 儲存慣例
		phone := fmt.Sprintf("+8869%08d", seq)                            // canonical E.164（TW 行動）
		members = append(members, model.Member{
			Username:     fmt.Sprintf("%s%03d", seedMemberPrefix, seq),
			PasswordHash: passwordHash,
			DisplayName:  fmt.Sprintf("玩家%03d", seq),
			ExternalID:   &ext,
			Email:        &email,
			Phone:        &phone,
			Status:       model.MemberStatusActive,
		})
	}
	return members
}

// buildDeposits 產生 n 筆「欄位值隨機但有效」的 deposit_records：
//   - player_id / player_name：隨機綁定傳入 members（members 須已具備真實 ID）
//   - amount：隨機 100..1,000,000（百元整數），恆 > 0（符合 DB CHECK amount > 0）
//   - currency：固定 TWD（DB CHECK currency IN ('TWD')，僅此一合法值，不可隨機）
//   - status / payment_method：隨機挑選合法 enum
//   - operator_ip：有 operator 時隨機挑合法非路由 INET；display_note / internal_note 隨機備註
//   - reference_no：唯一欄位，以 seedRefPrefix 序號編碼保證不撞（唯一性優先於隨機）
//   - operatorID 為 nil 時 operator_id / operator_ip / internal_note 留空（皆 nullable）
//
// 亂數以固定 seedRandSeed 驅動 → 結果可重現（見 seedRandSeed 註解）。
func buildDeposits(n int, members []model.Member, operatorID *uuid.UUID) []model.DepositRecord {
	// #nosec G404 -- 假資料產生非安全用途；固定種子刻意求可重現，不可用 crypto/rand
	rng := rand.New(rand.NewSource(seedRandSeed))
	recs := make([]model.DepositRecord, 0, n)

	for i := 0; i < n; i++ {
		m := members[rng.Intn(len(members))]
		amount := int64(rng.Intn(seedMaxAmountUnits)+1) * 100
		ref := fmt.Sprintf("%s%04d", seedRefPrefix, i+1)
		note := seedNotes[rng.Intn(len(seedNotes))]
		displayNote := fmt.Sprintf("%s（NT$%d）", note, amount)

		rec := model.DepositRecord{
			PlayerID:      m.ID,
			PlayerName:    seedPlayerName(m),
			Amount:        amount,
			Currency:      "TWD",
			Status:        seedStatuses[rng.Intn(len(seedStatuses))],
			PaymentMethod: seedMethods[rng.Intn(len(seedMethods))],
			DisplayNote:   &displayNote,
			ReferenceNo:   &ref,
		}

		if operatorID != nil {
			ip := seedOperatorIPs[rng.Intn(len(seedOperatorIPs))]
			internal := fmt.Sprintf("seed: %s（由管理員建立）", note)
			rec.OperatorID = operatorID
			rec.OperatorIP = &ip
			rec.InternalNote = &internal
		}

		recs = append(recs, rec)
	}
	return recs
}

// seedPlayerName 取玩家可顯示名稱：優先 DisplayName，未設定時退回 Username。
func seedPlayerName(m model.Member) string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Username
}
