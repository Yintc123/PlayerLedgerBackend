// Package schema_test validates the OpenAPI 3.1 schema file syntactically
// and semantically using kin-openapi（規格 §1.2 / §3.3 CI 整合）。
package schema_test

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestOpenAPISchema_Valid 驗證 schema/openapi.yaml 是合法的 OpenAPI 3.1 文件。
// CI 跑此 test 以確保 schema 變更未引入 syntax / 結構錯誤。
func TestOpenAPISchema_Valid(t *testing.T) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	doc, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("schema validation: %v", err)
	}
}
