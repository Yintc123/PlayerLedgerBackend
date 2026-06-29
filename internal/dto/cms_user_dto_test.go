package dto

import (
	"strings"
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
	assert.Equal(t, deletedAt.UTC().Format(time.RFC3339), *d.DeletedAt)
}

func TestFromCMSUser_TimestampsRFC3339UTC(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	u := &model.CMSUser{
		Base:     model.Base{ID: uuid.New(), CreatedAt: created, UpdatedAt: created},
		Username: "tz",
		Role:     "user",
	}
	d := FromCMSUser(u)

	// 不論輸入時區，輸出一律正規化為 UTC（Z 結尾）
	assert.Equal(t, created.UTC().Format(time.RFC3339), d.CreatedAt)
	assert.Equal(t, created.UTC().Format(time.RFC3339), d.UpdatedAt)
	assert.True(t, strings.HasSuffix(d.CreatedAt, "Z"), "created_at 應為 UTC（Z 結尾），got %s", d.CreatedAt)
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
