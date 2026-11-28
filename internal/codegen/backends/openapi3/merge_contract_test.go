package openapi3

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func TestParseMergePreservesOperationInputs(t *testing.T) {
	dir := t.TempDir()
	src := &sourceconfig.Source{Name: "api", OpenAPI3: &sourceconfig.OpenAPI3Config{}}
	for i, method := range []string{"get", "post", "get", "head", "options", "trace"} {
		base := "/v1"
		if i == 2 {
			base = "/v2"
		}
		file := fmt.Sprintf("%d.json", i)
		input := fmt.Sprintf(`{"servers":[{"url":%q}],"security":[{"key":[]}],"components":{"securitySchemes":{"key":{"type":"apiKey","in":"header","name":"X-Key"}}},"paths":{"/items/{id}":{"parameters":[{"in":"path","name":"id","required":true,"schema":{"type":"string"}},{"in":"header","name":"X-File","schema":{"type":"string","default":%q}}],%q:{"operationId":%q,"parameters":[{"in":"query","name":"id","schema":{"type":"string"}}],"responses":{"200":{}}}}}}`, base, file, method, file)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		src.OpenAPI3.Files = append(src.OpenAPI3.Files, file)
	}
	mod, err := Parse(src, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Operations) != 6 {
		t.Fatalf("operations = %d, want 6", len(mod.Operations))
	}
	for _, op := range mod.Operations {
		if len(op.Parameters) != 3 {
			t.Errorf("%s parameters = %+v", op.OperationID, op.Parameters)
			continue
		}
		if op.Parameters[2].Default != op.OperationID {
			t.Errorf("%s inherited another file's parameters: %+v", op.OperationID, op.Parameters)
		}
		if len(op.Security) != 1 {
			t.Errorf("%s security = %+v", op.OperationID, op.Security)
		}
		wantBase := "/v1"
		if op.OperationID == "2.json" {
			wantBase = "/v2"
		}
		if op.ServerBasePath != wantBase {
			t.Errorf("%s base = %q", op.OperationID, op.ServerBasePath)
		}
	}
}

func TestParseRejectsDuplicateOperationsDeterministically(t *testing.T) {
	dir := t.TempDir()
	input := `{"paths":{"/z":{"get":{"operationId":"z","responses":{}}},"/a":{"get":{"operationId":"a","responses":{}}}}}`
	for _, file := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src := &sourceconfig.Source{Name: "api", OpenAPI3: &sourceconfig.OpenAPI3Config{Files: []string{"a.json", "b.json"}}}
	_, err := Parse(src, dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate GET /a") {
		t.Fatalf("duplicate diagnosis = %v", err)
	}
}
