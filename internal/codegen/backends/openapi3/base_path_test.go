package openapi3

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func TestParse_SourceBasePath(t *testing.T) {
	for _, override := range []string{"", "/api/v1"} {
		t.Run(override, func(t *testing.T) {
			dir := t.TempDir()
			src := &sourceconfig.Source{Name: "api", OpenAPI3: &sourceconfig.OpenAPI3Config{}}
			src.BasePath = &override
			want := map[string]string{}
			for i := range 2 {
				file := fmt.Sprintf("spec%d.json", i)
				input := fmt.Sprintf(`{"openapi":"3.0.0","servers":[{"url":"/document%d"}],"paths":{"/items%d":{"get":{"operationId":"items%d","responses":{"200":{}}}},"/path%d":{"servers":[{"url":"/path-server"}],"get":{"operationId":"path%d","responses":{"200":{}}}},"/operation%d":{"servers":[{"url":"/path-server"}],"get":{"servers":[{"url":"/operation-server"}],"operationId":"operation%d","responses":{"200":{}}}}}}`, i, i, i, i, i, i, i)
				if err := os.WriteFile(filepath.Join(dir, file), []byte(input), 0o600); err != nil {
					t.Fatal(err)
				}
				src.OpenAPI3.Files = append(src.OpenAPI3.Files, file)
				for _, name := range []string{"items", "path", "operation"} {
					id := fmt.Sprintf("%s%d", name, i)
					want[id] = override + "/" + id
				}
			}
			mod, err := Parse(src, dir)
			if err != nil {
				t.Fatal(err)
			}
			specs := normalize.Normalize(mod)
			if len(specs) != len(want) {
				t.Fatalf("commands = %d, want %d", len(specs), len(want))
			}
			for _, spec := range specs {
				if spec.PathTpl != want[spec.OperationID] {
					t.Errorf("%s path template = %q, want %q", spec.OperationID, spec.PathTpl, want[spec.OperationID])
				}
			}
		})
	}
}
