package pagination

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPageRequest_SetDefaults(t *testing.T) {
	p := &PageRequest{}
	p.SetDefaults()
	assert.Equal(t, p.Page, 1)
	assert.Equal(t, p.PageSize, 20)
}

func TestPageRequest_Offset(t *testing.T) {
	p := &PageRequest{Page: 3, PageSize: 10}
	assert.Equal(t, p.Offset(), 20)
}

func TestCalcPageMeta(t *testing.T) {
	meta := CalcPageMeta(1, 20, 50)
	assert.Equal(t, meta.Page, 1)
	assert.Equal(t, meta.PageSize, 20)
	assert.Equal(t, meta.Total, int64(50))
	assert.Equal(t, meta.TotalPage, 3)
}

func TestCalcPageMeta_Zero(t *testing.T) {
	meta := CalcPageMeta(1, 20, 0)
	assert.Equal(t, meta.TotalPage, 1)
}
