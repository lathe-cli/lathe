package swagger

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
			src := &sourceconfig.Source{Name: "api", Swagger: &sourceconfig.SwaggerConfig{}}
			src.BasePath = &override
			want := map[string]string{}
			for i := range 2 {
				file := fmt.Sprintf("spec%d.json", i)
				input := fmt.Sprintf(`{"swagger":"2.0","basePath":"/document%d","paths":{"/items%d":{"get":{"operationId":"items%d","responses":{"200":{}}}}}}`, i, i, i)
				if err := os.WriteFile(filepath.Join(dir, file), []byte(input), 0o600); err != nil {
					t.Fatal(err)
				}
				src.Swagger.Files = append(src.Swagger.Files, file)
				want[fmt.Sprintf("items%d", i)] = fmt.Sprintf("%s/items%d", override, i)
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
