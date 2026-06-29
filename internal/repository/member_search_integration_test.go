//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yintengching/playerledger/internal/model"
)

// newSearchMember 建立一名可搜尋的玩家；opts 可覆寫欄位。
func newSearchMember(t *testing.T, db *gorm.DB, username, display string, opts ...func(*model.Member)) *model.Member {
	t.Helper()
	m := &model.Member{
		Base:         model.Base{ID: uuid.New()},
		Username:     username,
		PasswordHash: "$2a$12$hash",
		DisplayName:  display,
		Status:       model.MemberStatusActive,
	}
	for _, o := range opts {
		o(m)
	}
	require.NoError(t, db.Create(m).Error)
	return m
}

func strPtr(s string) *string { return &s }

// TestMemberRepository_Search_DisplayName前綴_大小寫不敏感
func TestMemberRepository_Search_DisplayName前綴_大小寫不敏感(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	newSearchMember(t, db, "u1", "Alice")
	newSearchMember(t, db, "u2", "alfred")
	newSearchMember(t, db, "u3", "Bob")

	got, err := repo.Search(ctx, PlayerSearchFilter{DisplayNamePrefix: strPtr("AL"), Limit: 20})
	require.NoError(t, err)
	assert.Len(t, got, 2, "AL 前綴（大小寫不敏感）應命中 Alice 與 alfred")
}

// TestMemberRepository_Search_Email前綴_Phone精確_ExternalID精確
func TestMemberRepository_Search_Email前綴_Phone精確_ExternalID精確(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	newSearchMember(t, db, "u1", "P1", func(m *model.Member) {
		m.Email = strPtr("wang@example.com")
		m.Phone = strPtr("+886912345678")
		m.ExternalID = strPtr("EXT-1")
	})
	newSearchMember(t, db, "u2", "P2", func(m *model.Member) {
		m.Email = strPtr("other@example.com")
	})

	byEmail, err := repo.Search(ctx, PlayerSearchFilter{EmailPrefix: strPtr("wang"), Limit: 20})
	require.NoError(t, err)
	require.Len(t, byEmail, 1)
	assert.Equal(t, "P1", byEmail[0].DisplayName)

	byPhone, err := repo.Search(ctx, PlayerSearchFilter{Phone: strPtr("+886912345678"), Limit: 20})
	require.NoError(t, err)
	require.Len(t, byPhone, 1)
	assert.Equal(t, "P1", byPhone[0].DisplayName)

	byExt, err := repo.Search(ctx, PlayerSearchFilter{ExternalID: strPtr("EXT-1"), Limit: 20})
	require.NoError(t, err)
	require.Len(t, byExt, 1)
	assert.Equal(t, "P1", byExt[0].DisplayName)
}

// TestMemberRepository_Search_AND組合
func TestMemberRepository_Search_AND組合(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	newSearchMember(t, db, "u1", "Alice", func(m *model.Member) { m.Email = strPtr("alice@example.com") })
	newSearchMember(t, db, "u2", "Alice", func(m *model.Member) { m.Email = strPtr("other@example.com") })

	got, err := repo.Search(ctx, PlayerSearchFilter{
		DisplayNamePrefix: strPtr("Alice"),
		EmailPrefix:       strPtr("alice"),
		Limit:             20,
	})
	require.NoError(t, err)
	require.Len(t, got, 1, "display_name 與 email 須同時滿足（AND）")
	assert.Equal(t, "alice@example.com", *got[0].Email)
}

// TestMemberRepository_Search_軟刪除排除
func TestMemberRepository_Search_軟刪除排除(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	m := newSearchMember(t, db, "u1", "Ghost")
	require.NoError(t, db.Delete(m).Error)

	got, err := repo.Search(ctx, PlayerSearchFilter{DisplayNamePrefix: strPtr("Ghost"), Limit: 20})
	require.NoError(t, err)
	assert.Empty(t, got, "軟刪除玩家不應出現在搜尋結果")
}

// TestMemberRepository_Search_LIKE萬用字元跳脫
func TestMemberRepository_Search_LIKE萬用字元跳脫(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	newSearchMember(t, db, "u1", "Normal")

	// "%" 應被當字面值跳脫，不應 match 任意字串
	got, err := repo.Search(ctx, PlayerSearchFilter{DisplayNamePrefix: strPtr("%"), Limit: 20})
	require.NoError(t, err)
	assert.Empty(t, got, "% 應被跳脫為字面值，不應萬用比對到 Normal")
}

// TestMemberRepository_Search_Keyset翻頁_不重不漏
func TestMemberRepository_Search_Keyset翻頁_不重不漏(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		newSearchMember(t, db, uuid.NewString(), "Page", func(m *model.Member) { m.CreatedAt = ts })
	}

	seen := map[uuid.UUID]bool{}
	var after *Keyset
	for {
		page, err := repo.Search(ctx, PlayerSearchFilter{DisplayNamePrefix: strPtr("Page"), After: after, Limit: 2})
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		// 驗證頁內排序為 created_at DESC
		for i := 1; i < len(page); i++ {
			assert.False(t, page[i].CreatedAt.After(page[i-1].CreatedAt), "頁內須 created_at DESC")
		}
		for _, m := range page {
			assert.False(t, seen[m.ID], "玩家 %s 不應跨頁重複", m.ID)
			seen[m.ID] = true
		}
		last := page[len(page)-1]
		after = &Keyset{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	assert.Len(t, seen, 5, "keyset 翻頁應走訪全部 5 名玩家，不重不漏")
}

// TestMemberRepository_Search_同CreatedAt_由ID穩定排序
func TestMemberRepository_Search_同CreatedAt_由ID穩定排序(t *testing.T) {
	db := WithTx(t)
	repo := NewMemberRepository(db)
	ctx := context.Background()

	ts := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		newSearchMember(t, db, uuid.NewString(), "Tie", func(m *model.Member) { m.CreatedAt = ts })
	}

	seen := map[uuid.UUID]bool{}
	var after *Keyset
	for {
		page, err := repo.Search(ctx, PlayerSearchFilter{DisplayNamePrefix: strPtr("Tie"), After: after, Limit: 1})
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		m := page[0]
		assert.False(t, seen[m.ID], "同 created_at 下仍應由 id tie-break，不重複")
		seen[m.ID] = true
		after = &Keyset{CreatedAt: m.CreatedAt, ID: m.ID}
	}
	assert.Len(t, seen, 3)
}
