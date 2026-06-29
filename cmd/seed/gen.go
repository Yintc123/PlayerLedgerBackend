package main

import (
	"fmt"

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
)

// seedStatuses / seedMethods 列出所有合法 enum，round-robin 覆蓋以利前端測試各狀態與付款方式。
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
)

// buildMembers 產生 n 筆 deterministic、合規的 seed 玩家。
// 所有玩家共用同一組 bcrypt password hash（dev 方便登入測試）。
// username 形如 seed_player_001，保證唯一且不超過 64 字元上限。
func buildMembers(n int, passwordHash string) []model.Member {
	members := make([]model.Member, 0, n)
	for i := 0; i < n; i++ {
		members = append(members, model.Member{
			Username:     fmt.Sprintf("%s%03d", seedMemberPrefix, i+1),
			PasswordHash: passwordHash,
		})
	}
	return members
}

// buildDeposits 產生 n 筆合規 deposit_records：
//   - player_id / player_name round-robin 綁定傳入 members（members 須已具備真實 ID）
//   - amount 恆 > 0、currency 固定 TWD（皆符合 DB CHECK 約束）
//   - status / payment_method 輪巡所有合法 enum
//   - reference_no 以 seedRefPrefix 編號，保證唯一且可辨識/冪等
//   - operatorID 為 nil 時 operator_id / operator_ip 留空（兩者皆 nullable）
func buildDeposits(n int, members []model.Member, operatorID *uuid.UUID) []model.DepositRecord {
	recs := make([]model.DepositRecord, 0, n)

	var operatorIP *string
	if operatorID != nil {
		ip := "203.0.113.10" // TEST-NET-3 範例位址，合法 INET 值
		operatorIP = &ip
	}

	for i := 0; i < n; i++ {
		m := members[i%len(members)]
		amount := int64((i%10)+1) * 1000 // 1000..10000，恆 > 0
		ref := fmt.Sprintf("%s%04d", seedRefPrefix, i+1)
		note := fmt.Sprintf("seed deposit #%d", i+1)

		recs = append(recs, model.DepositRecord{
			PlayerID:      m.ID,
			PlayerName:    m.Username,
			Amount:        amount,
			Currency:      "TWD",
			Status:        seedStatuses[i%len(seedStatuses)],
			PaymentMethod: seedMethods[i%len(seedMethods)],
			OperatorID:    operatorID,
			OperatorIP:    operatorIP,
			DisplayNote:   &note,
			ReferenceNo:   &ref,
		})
	}
	return recs
}
