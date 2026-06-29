package dto

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/model"
)

func strptr(s string) *string { return &s }

func TestFromMember_NoMask_CopiesAllFields(t *testing.T) {
	id := uuid.New()
	created := time.Date(2026, 6, 1, 8, 30, 0, 0, time.UTC)
	active := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	m := &model.Member{
		Base:         model.Base{ID: id, CreatedAt: created},
		ExternalID:   strptr("ext-1"),
		DisplayName:  "Alice",
		Email:        strptr("alice@example.com"),
		Phone:        strptr("0912345678"),
		Status:       model.MemberStatusActive,
		LastActiveAt: &active,
	}

	d := FromMember(m, false)

	assert.Equal(t, id.String(), d.PlayerID)
	assert.Equal(t, "ext-1", *d.ExternalID)
	assert.Equal(t, "Alice", d.DisplayName)
	assert.Equal(t, "active", d.Status)
	assert.Equal(t, created.Format(time.RFC3339), d.RegisteredAt)
	require.NotNil(t, d.Email)
	assert.Equal(t, "alice@example.com", *d.Email)
	require.NotNil(t, d.Phone)
	assert.Equal(t, "0912345678", *d.Phone)
	require.NotNil(t, d.LastActiveAt)
	assert.Equal(t, active.Format(time.RFC3339), *d.LastActiveAt)
}

func TestFromMember_Mask_MasksEmailAndPhoneOnly(t *testing.T) {
	m := &model.Member{
		Base:        model.Base{ID: uuid.New()},
		DisplayName: "Bob",
		Email:       strptr("bob@example.com"),
		Phone:       strptr("0912345678"),
		Status:      model.MemberStatusFrozen,
	}

	d := FromMember(m, true)

	assert.Equal(t, "b***@example.com", *d.Email)
	assert.Equal(t, "0912***5678", *d.Phone)
	assert.Equal(t, "Bob", d.DisplayName, "DisplayName 不受遮罩")
	assert.Equal(t, "frozen", d.Status)
}

func TestFromMember_NilOptionalFields_StayNil(t *testing.T) {
	m := &model.Member{
		Base:        model.Base{ID: uuid.New()},
		DisplayName: "NoContact",
		Status:      model.MemberStatusClosed,
	}

	d := FromMember(m, true)

	assert.Nil(t, d.Email)
	assert.Nil(t, d.Phone)
	assert.Nil(t, d.ExternalID)
	assert.Nil(t, d.LastActiveAt)
}

func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"ab@example.com": "a***@example.com",
		"abc@x.io":       "a***@x.io",
		"a@example.com":  "***", // 本地長度 1 → at=1 → 整段遮罩
		"@example.com":   "***", // at=0
		"noatsign":       "***", // 無 @
		"":               "***",
	}
	for in, want := range cases {
		assert.Equalf(t, want, maskEmail(in), "maskEmail(%q)", in)
	}
}

func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"0912345678": "0912***5678",
		"12345678":   "1234***5678", // 剛好 8 碼
		"1234567":    "***",         // 7 碼 < 8
		"":           "***",
	}
	for in, want := range cases {
		assert.Equalf(t, want, maskPhone(in), "maskPhone(%q)", in)
	}
}

func TestFromMemberList_Empty_ReturnsNonNilEmptySlice(t *testing.T) {
	got := FromMemberList(nil, false)
	assert.NotNil(t, got)
	assert.Len(t, got, 0)
}

func TestFromMemberList_PreservesOrderAndMask(t *testing.T) {
	members := []*model.Member{
		{Base: model.Base{ID: uuid.New()}, DisplayName: "A", Email: strptr("aa@x.com"), Status: model.MemberStatusActive},
		{Base: model.Base{ID: uuid.New()}, DisplayName: "B", Email: strptr("bb@x.com"), Status: model.MemberStatusActive},
	}
	got := FromMemberList(members, true)
	require.Len(t, got, 2)
	assert.Equal(t, "A", got[0].DisplayName)
	assert.Equal(t, "a***@x.com", *got[0].Email)
	assert.Equal(t, "B", got[1].DisplayName)
}
