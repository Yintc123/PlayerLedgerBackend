package dto

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/internal/model"
	"gorm.io/gorm"
)

func TestFromCMSUser_NotDeleted_NoDeletedAt(t *testing.T) {
	u := &model.CMSUser{
		Base:     model.Base{ID: uuid.New(), CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Username: "admin",
		Role:     "admin",
	}
	d := FromCMSUser(u)

	assert.Equal(t, u.ID.String(), d.ID)
	assert.Equal(t, "admin", d.Username)
	assert.Equal(t, "admin", d.Role)
	assert.Nil(t, d.DeletedAt, "未刪除不應帶 deleted_at")
}

func TestFromCMSUser_SoftDeleted_CarriesDeletedAt(t *testing.T) {
	deletedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	u := &model.CMSUser{
		Base: model.Base{
			ID:        uuid.New(),
			DeletedAt: gorm.DeletedAt{Time: deletedAt, Valid: true},
		},
		Username: "gone",
		Role:     "viewer",
	}
	d := FromCMSUser(u)

	require.NotNil(t, d.DeletedAt)
	assert.Equal(t, deletedAt, *d.DeletedAt)
}

func TestFromCMSUser_NeverLeaksPasswordHash(t *testing.T) {
	// 結構上即無 password 欄位；此測試釘住「DTO 不得新增敏感欄位」的契約。
	u := &model.CMSUser{Base: model.Base{ID: uuid.New()}, Username: "x", PasswordHash: "secret-hash", Role: "user"}
	d := FromCMSUser(u)
	assert.NotContains(t, d.Username, "secret-hash")
}

func TestFromCMSUserList_Empty_NonNil(t *testing.T) {
	got := FromCMSUserList(nil)
	assert.NotNil(t, got)
	assert.Len(t, got, 0)
}

func TestFromCMSUserList_PreservesOrder(t *testing.T) {
	us := []model.CMSUser{
		{Base: model.Base{ID: uuid.New()}, Username: "a", Role: "admin"},
		{Base: model.Base{ID: uuid.New()}, Username: "b", Role: "user"},
	}
	got := FromCMSUserList(us)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Username)
	assert.Equal(t, "b", got[1].Username)
}
