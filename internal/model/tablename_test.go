package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTableNames 釘住 GORM TableName() 對映的實體資料表名，
// 避免日後改 struct 名時不慎改動到資料表對映。
func TestTableNames(t *testing.T) {
	assert.Equal(t, "cms_users", CMSUser{}.TableName())
	assert.Equal(t, "members", Member{}.TableName())
	assert.Equal(t, "deposit_records", DepositRecord{}.TableName())
}
