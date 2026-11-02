package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestDryRun_BodyNumberPrecisionAndRedaction(t *testing.T) {
	isolateRuntime(t)
	for _, value := range []string{"18446744073709551615", "9223372036854775809", "9007199254740993"} {
		for _, mode := range []string{"plain", "redacted"} {
			t.Run(value+"/"+mode, func(t *testing.T) {
				var stdout bytes.Buffer
				root := newExecutionRoot("raw")
				root.SetOut(&stdout)
				root.SetErr(io.Discard)
				mustBuild(t, root, "demo", []CommandSpec{{
					Group: "Values", Use: "create", Method: "POST", PathTpl: "/values",
					Security: &SecurityHint{Public: true},
					RequestBody: &RequestBody{MediaType: "application/json", Schema: &SchemaSpec{
						Type: "object", Properties: map[string]*SchemaSpec{
							"v":            {Type: "string", Format: "uint64"},
							"confidential": {Format: "password"},
						},
					}},
				}})
				args := []string{"demo", "values", "create", "--hostname", "http://127.0.0.1:1", "--dry-run",
					"--set", "v=" + value, "--set", "nested.v=" + value, "--set", "items[0]=" + value,
					"--set", "flag=true", "--set", "empty=null", "--set", "fraction=1.5", "--set", "text=plain"}
				if mode == "redacted" {
					args = append(args, "--set", "password=private-value", "--set", "confidential="+value,
						"--set", "envVars[0].key=COUNT", "--set", "envVars[0].value="+value)
				}
				root.SetArgs(args)
				testutil.NoError(t, root.Execute())
				var out struct {
					Body map[string]json.RawMessage `json:"body"`
				}
				testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
				for field, want := range map[string]string{
					"v": value, "nested": `{"v":` + value + `}`, "items": `[` + value + `]`,
					"flag": "true", "empty": "null", "fraction": "1.5", "text": `"plain"`,
				} {
					var compact bytes.Buffer
					testutil.NoError(t, json.Compact(&compact, out.Body[field]))
					testutil.Require(t, compact.String() == want, "%s = %s, want %s", field, compact.String(), want)
				}
				if mode == "redacted" {
					testutil.Require(t, string(out.Body["password"]) == `"***"` && string(out.Body["confidential"]) == `"***"`, "sensitive fields = %s", stdout.String())
					var compact bytes.Buffer
					testutil.NoError(t, json.Compact(&compact, out.Body["envVars"]))
					testutil.Require(t, compact.String() == `[{"key":"COUNT","value":"***"}]`, "envVars = %s", compact.String())
					testutil.Require(t, !strings.Contains(stdout.String(), "private-value"), "password leaked: %s", stdout.String())
				}
			})
		}
	}
}
