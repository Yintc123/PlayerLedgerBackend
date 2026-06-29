package main

import (
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yintengching/playerledger/internal/model"
)

func TestBuildMembers_Count20_AllCompliantAndUnique(t *testing.T) {
	members := buildMembers(20, "bcrypt-hash-placeholder")

	require.Len(t, members, 20)

	seen := make(map[string]bool)
	for _, m := range members {
		assert.NotEmpty(t, m.Username)
		assert.LessOrEqual(t, len(m.Username), 64, "username 不可超過 VARCHAR(64)")
		assert.NotEmpty(t, m.PasswordHash)
		assert.LessOrEqual(t, len(m.PasswordHash), 72, "password_hash 不可超過 VARCHAR(72)")
		assert.False(t, seen[m.Username], "username 必須唯一：%s", m.Username)
		seen[m.Username] = true
	}
}

func TestBuildDeposits_Count50_AllCompliantAndUniqueRef(t *testing.T) {
	members := buildMembers(20, "h")
	for i := range members {
		members[i].ID = uuid.New()
	}
	operatorID := uuid.New()

	recs := buildDeposits(50, members, &operatorID)

	require.Len(t, recs, 50)

	validStatus := make(map[model.DepositStatus]bool)
	for _, s := range seedStatuses {
		validStatus[s] = true
	}
	validMethod := make(map[model.PaymentMethod]bool)
	for _, m := range seedMethods {
		validMethod[m] = true
	}

	refs := make(map[string]bool)
	for _, r := range recs {
		assert.Greater(t, r.Amount, int64(0), "amount 必須 > 0（DB CHECK）")
		assert.Equal(t, "TWD", r.Currency, "currency 僅允許 TWD")
		assert.True(t, validStatus[r.Status], "status 必須為合法 enum：%s", r.Status)
		assert.True(t, validMethod[r.PaymentMethod], "payment_method 必須為合法 enum：%s", r.PaymentMethod)
		assert.NotEqual(t, uuid.Nil, r.PlayerID, "player_id 不可為空（NOT NULL FK）")
		assert.NotEmpty(t, r.PlayerName)
		require.NotNil(t, r.ReferenceNo)
		assert.False(t, refs[*r.ReferenceNo], "reference_no 必須唯一：%s", *r.ReferenceNo)
		refs[*r.ReferenceNo] = true
	}
}

func TestBuildDeposits_RandomFields_StayValidAndVaried(t *testing.T) {
	members := buildMembers(20, "h")
	for i := range members {
		members[i].ID = uuid.New()
	}
	operatorID := uuid.New()

	recs := buildDeposits(50, members, &operatorID)

	amounts := make(map[int64]bool)
	statuses := make(map[model.DepositStatus]bool)
	for _, r := range recs {
		// 隨機但有效：amount 落在 100..1,000,000 的百元整數
		assert.GreaterOrEqual(t, r.Amount, int64(100))
		assert.LessOrEqual(t, r.Amount, int64(seedMaxAmountUnits*100))
		assert.Zero(t, r.Amount%100, "amount 應為百元整數：%d", r.Amount)

		// 有 operator 時 operator_ip 為合法 IP、internal_note 有值
		require.NotNil(t, r.OperatorIP, "有 operator 時 operator_ip 不應為空")
		assert.NotNil(t, net.ParseIP(*r.OperatorIP), "operator_ip 必須為合法 INET：%s", *r.OperatorIP)
		require.NotNil(t, r.InternalNote)
		require.NotNil(t, r.DisplayNote)

		amounts[r.Amount] = true
		statuses[r.Status] = true
	}

	// 證明確實「隨機」：50 筆不會全為同一金額、同一狀態
	assert.Greater(t, len(amounts), 1, "amount 應有變化（隨機），而非全部相同")
	assert.Greater(t, len(statuses), 1, "status 應有變化（隨機），而非全部相同")
}

func TestBuildDeposits_FixedSeed_Reproducible(t *testing.T) {
	members := buildMembers(20, "h")
	for i := range members {
		members[i].ID = uuid.New()
	}
	operatorID := uuid.New()

	first := buildDeposits(50, members, &operatorID)
	second := buildDeposits(50, members, &operatorID)

	// 固定種子 → 兩次產生完全一致，CI 重跑可重現
	assert.Equal(t, first, second)
}

func TestBuildDeposits_NilOperator_LeavesOperatorFieldsNull(t *testing.T) {
	members := buildMembers(1, "h")
	members[0].ID = uuid.New()

	recs := buildDeposits(3, members, nil)

	for _, r := range recs {
		assert.Nil(t, r.OperatorID, "無 operator 時 operator_id 應留空")
		assert.Nil(t, r.OperatorIP, "無 operator 時 operator_ip 應留空")
	}
}
