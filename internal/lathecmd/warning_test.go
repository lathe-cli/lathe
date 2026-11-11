package lathecmd

import (
	"bytes"
	"strings"
	"testing"
	"unicode"
)

func TestCodegenWarningEscapesSpecControls(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, ".cache/specs-sync/acme/openapi.yaml", `openapi: "3.0.3"
paths:
  /things:
    get:
      operationId: "getThing\nwarning: injected\u001b[31m"
      parameters:
        - name: output
          in: query
          schema:
            type: string
      responses:
        "200":
          description: OK
`)
	var output bytes.Buffer
	if err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &output); err != nil {
		t.Fatal(err)
	}
	warning := output.String()
	if warning == "" || !strings.HasSuffix(warning, "\n") {
		t.Fatalf("expected a complete warning line: %q", warning)
	}
	if strings.IndexFunc(strings.TrimSuffix(warning, "\n"), unicode.IsControl) >= 0 {
		t.Fatalf("spec control characters escaped the warning line: %q", warning)
	}
}
