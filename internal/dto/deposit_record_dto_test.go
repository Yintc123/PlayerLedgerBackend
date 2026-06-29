package dto

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/model"
)

func sampleRecord() *model.DepositRecord {
	opID := uuid.New()
	created := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	return &model.DepositRecord{
		ID:            uuid.New(),
		PlayerID:      uuid.New(),
		PlayerName:    "Alice",
		Amount:        1000,
		Currency:      "TWD",
		Status:        model.DepositStatusCompleted,
		PaymentMethod: model.PaymentMethodBankTransfer,
		OperatorID:    &opID,
		OperatorIP:    strptr("10.0.0.1"),
		InternalNote:  strptr("internal"),
		DisplayNote:   strptr("display"),
		ReferenceNo:   strptr("REF-1"),
		CreatedAt:     created,
		UpdatedAt:     created,
	}
}

func TestFromDepositRecord_FullFields(t *testing.T) {
	r := sampleRecord()
	d := FromDepositRecord(r)

	assert.Equal(t, r.ID.String(), d.ID)
	assert.Equal(t, r.PlayerID.String(), d.PlayerID)
	assert.Equal(t, "Alice", d.PlayerName)
	assert.Equal(t, int64(1000), d.Amount)
	assert.Equal(t, "completed", d.Status)
	assert.Equal(t, "bank_transfer", d.PaymentMethod)
	require.NotNil(t, d.OperatorID)
	assert.Equal(t, r.OperatorID.String(), *d.OperatorID)
	assert.Equal(t, "internal", *d.InternalNote)
	assert.Equal(t, r.CreatedAt.Format(time.RFC3339), d.CreatedAt)
}

func TestFromDepositRecord_NilOperatorID(t *testing.T) {
	r := sampleRecord()
	r.OperatorID = nil
	d := FromDepositRecord(r)
	assert.Nil(t, d.OperatorID)
}

func TestFromDepositRecordPublic_HidesInternalFields(t *testing.T) {
	r := sampleRecord()
	d := FromDepositRecordPublic(r)

	assert.Equal(t, r.ID.String(), d.ID)
	assert.Equal(t, int64(1000), d.Amount)
	assert.Equal(t, "display", *d.DisplayNote)
	// 公開 DTO 不含 player_id / operator / internal_note / reference_no（型別上即無這些欄位）。
	assert.Equal(t, "completed", d.Status)
}

func TestFromDepositRecordList_Empty_NonNil(t *testing.T) {
	assert.Equal(t, []DepositRecordDTO{}, FromDepositRecordList(nil))
}

func TestFromDepositRecordPublicList_Empty_NonNil(t *testing.T) {
	assert.Equal(t, []DepositRecordPublicDTO{}, FromDepositRecordPublicList(nil))
}

func TestFromDepositRecordList_PreservesOrder(t *testing.T) {
	r1, r2 := sampleRecord(), sampleRecord()
	r1.PlayerName, r2.PlayerName = "First", "Second"
	got := FromDepositRecordList([]*model.DepositRecord{r1, r2})
	require.Len(t, got, 2)
	assert.Equal(t, "First", got[0].PlayerName)
	assert.Equal(t, "Second", got[1].PlayerName)
}
