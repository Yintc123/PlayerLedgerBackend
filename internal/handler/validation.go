package handler

import (
	"reflect"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// init 讓 binding validator 的錯誤 details[].field 使用 JSON 欄位名（snake_case），
// 對齊 OpenAPI ErrorResponse.details 慣例（前端依 field 判定，需與 schema 欄位名一致）。
// 在 handler 套件被載入時即註冊，production 與測試行為一致。
func init() {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
}
