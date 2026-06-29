package main

import (
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

func TestBuildDeposits_NilOperator_LeavesOperatorFieldsNull(t *testing.T) {
	members := buildMembers(1, "h")
	members[0].ID = uuid.New()

	recs := buildDeposits(3, members, nil)

	for _, r := range recs {
		assert.Nil(t, r.OperatorID, "無 operator 時 operator_id 應留空")
		assert.Nil(t, r.OperatorIP, "無 operator 時 operator_ip 應留空")
	}
}
