package lathe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func snipTestOptions() RunOptions {
	return RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n  short: test cli\n"),
		Mount: func(root *cobra.Command) error {
			return runtime.BuildFlat(root, "core", []runtime.CommandSpec{
				{
					Group:       "Users",
					Use:         "list",
					Short:       "List users",
					OperationID: "Users_List",
					Method:      "GET",
					PathTpl:     "/users",
					Params: []runtime.ParamSpec{
						{Name: "status", Flag: "status", In: runtime.InQuery, GoType: "string"},
						{Name: "limit", Flag: "limit", In: runtime.InQuery, GoType: "int64"},
					},
				},
				{
					Group:       "Users",
					Use:         "update",
					Short:       "Update user",
					OperationID: "Users_Update",
					Method:      "PATCH",
					PathTpl:     "/users/{id}",
					RequestBody: &runtime.RequestBody{MediaType: "application/json"},
					Params: []runtime.ParamSpec{
						{Name: "id", Flag: "id", In: runtime.InPath, GoType: "string", Required: true},
					},
				},
				{
					Group:       "Users",
					Use:         "remove",
					Short:       "Remove user",
					OperationID: "Users_Remove",
					Method:      "DELETE",
					PathTpl:     "/users/{id}",
					Params: []runtime.ParamSpec{
						{Name: "id", Flag: "id", Argument: "id", In: runtime.InPath, GoType: "string", Required: true},
						{Name: "reason", Flag: "reason", Argument: "reason", In: runtime.InQuery, GoType: "string"},
						{Name: "note", Flag: "note", In: runtime.InQuery, GoType: "string"},
					},
				},
			})
		},
	}
}

func runSnipCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(snipTestOptions(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestSnipLifecycleAndParameterOverride(t *testing.T) {
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("MYCTL_HOST", "api.example.com")

	code, _, stderr := runSnipCLI(t, "snip", "set", "active-users", "users list --status active --limit 20", "-d", "Active users")
	testutil.Require(t, code == runtime.ExitOK, "set active-users exit=%d stderr=%s", code, stderr)

	code, _, stderr = runSnipCLI(t, "snip", "set", "patch-user", "--", "users", "update", "--id", "$1", "--set", "role=$2", "--set-str", "note=hello world $$100")
	testutil.Require(t, code == runtime.ExitOK, "set patch-user exit=%d stderr=%s", code, stderr)

	code, listOut, stderr := runSnipCLI(t, "-o", "json", "snip", "list")
	testutil.Require(t, code == runtime.ExitOK, "list exit=%d stderr=%s", code, stderr)
	var listed snipListOutput
	testutil.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	testutil.Require(t, len(listed.Snips) == 2 && listed.Snips[0].Name == "active-users" && listed.Snips[1].Name == "patch-user", "listed = %+v", listed)

	code, catalogOut, stderr := runSnipCLI(t, "commands", "--json")
	testutil.Require(t, code == runtime.ExitOK, "commands exit=%d stderr=%s", code, stderr)
	testutil.Require(t, !strings.Contains(catalogOut, "active-users") && !strings.Contains(catalogOut, "patch-user") && !strings.Contains(catalogOut, `"snip"`), "catalog leaked snips:\n%s", catalogOut)

	code, searchOut, stderr := runSnipCLI(t, "search", "active-users", "--json")
	testutil.Require(t, code == runtime.ExitOK, "search exit=%d stderr=%s", code, stderr)
	testutil.Require(t, !strings.Contains(searchOut, "active-users"), "search leaked snips:\n%s", searchOut)

	code, previewOut, stderr := runSnipCLI(t, "--hostname", "staging.example.com", "snip", "active-users", "--limit", "50", "--dry-run")
	testutil.Require(t, code == runtime.ExitOK, "run active-users exit=%d stderr=%s", code, stderr)
	var listPreview struct {
		Method   string `json:"method"`
		URL      string `json:"url"`
		Hostname string `json:"hostname"`
	}
	testutil.NoError(t, json.Unmarshal([]byte(previewOut), &listPreview))
	testutil.Require(t, listPreview.Method == "GET" && listPreview.Hostname == "staging.example.com", "preview = %+v", listPreview)
	testutil.Require(t, listPreview.URL == "https://staging.example.com/users?limit=50&status=active", "url = %q", listPreview.URL)

	code, patchOut, stderr := runSnipCLI(t, "snip", "run", "patch-user", "usr_42", "admin", "--set", "role=owner", "--dry-run")
	testutil.Require(t, code == runtime.ExitOK, "run patch-user exit=%d stderr=%s", code, stderr)
	var patchPreview struct {
		Method string         `json:"method"`
		URL    string         `json:"url"`
		Body   map[string]any `json:"body"`
	}
	testutil.NoError(t, json.Unmarshal([]byte(patchOut), &patchPreview))
	testutil.Require(t, patchPreview.Method == "PATCH" && patchPreview.URL == "https://api.example.com/users/usr_42", "patch preview = %+v", patchPreview)
	testutil.Require(t, patchPreview.Body["role"] == "owner" && patchPreview.Body["note"] == "hello world $100", "body = %#v", patchPreview.Body)

	code, _, stderr = runSnipCLI(t, "snip", "delete", "active-users")
	testutil.Require(t, code == runtime.ExitOK, "delete exit=%d stderr=%s", code, stderr)
	code, _, _ = runSnipCLI(t, "snip", "active-users")
	testutil.Require(t, code == runtime.ExitUsage, "deleted snip exit=%d, want %d", code, runtime.ExitUsage)
}

func TestSnipRejectsFlagSwallowingIntoPlaceholders(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("MYCTL_HOST", srv.URL)

	code, _, stderr := runSnipCLI(t, "snip", "set", "set-role", "users update --id $1 --set role=$2")
	testutil.Require(t, code == runtime.ExitOK, "set exit=%d stderr=%s", code, stderr)

	code, _, _ = runSnipCLI(t, "snip", "set-role", "usr_1", "--dry-run")
	testutil.Require(t, code == runtime.ExitUsage, "missing $2 before --dry-run exit=%d, want %d", code, runtime.ExitUsage)
	testutil.Require(t, hits.Load() == 0, "server received %d unexpected request(s)", hits.Load())

	code, previewOut, stderr := runSnipCLI(t, "snip", "set-role", "--", "usr_1", "--literal-role", "--dry-run")
	testutil.Require(t, code == runtime.ExitOK, "escaped placeholder exit=%d stderr=%s", code, stderr)
	testutil.Require(t, hits.Load() == 0, "dry-run hit server")
	testutil.Require(t, strings.Contains(previewOut, `"role": "--literal-role"`), "previewOut = %s", previewOut)

	code, _, stderr = runSnipCLI(t, "snip", "set", "del-dash", "--", "users", "remove", "--", "$1")
	testutil.Require(t, code == runtime.ExitOK, "set del-dash exit=%d stderr=%s", code, stderr)

	code, previewOut, stderr = runSnipCLI(t, "snip", "del-dash", "usr_99", "--dry-run")
	testutil.Require(t, code == runtime.ExitOK, "del-dash --dry-run exit=%d stderr=%s", code, stderr)
	testutil.Require(t, hits.Load() == 0, "del-dash --dry-run hit server")
	testutil.Require(t, strings.HasSuffix(strings.TrimSpace(previewOut), "}") && strings.Contains(previewOut, "/users/usr_99") && !strings.Contains(previewOut, "reason="), "del-dash preview = %s", previewOut)

	code, helpOut, stderr := runSnipCLI(t, "snip", "del-dash", "--help")
	testutil.Require(t, code == runtime.ExitOK, "del-dash --help exit=%d stderr=%s", code, stderr)
	testutil.Require(t, hits.Load() == 0 && strings.Contains(helpOut, "Remove user"), "del-dash --help hit=%d out=%s", hits.Load(), helpOut)

	code, _, _ = runSnipCLI(t, "snip", "del-dash", "--note", "-h")
	testutil.Require(t, code == runtime.ExitUsage, "missing $1 with --note -h exit=%d, want %d", code, runtime.ExitUsage)
	testutil.Require(t, hits.Load() == 0, "--note -h hit server")

	code, previewOut, stderr = runSnipCLI(t, "snip", "del-dash", "usr_42", "--note", "-h", "--dry-run")
	testutil.Require(t, code == runtime.ExitOK, "del-dash usr_42 --note -h --dry-run exit=%d stderr=%s", code, stderr)
	testutil.Require(t, hits.Load() == 0, "del-dash usr_42 --note -h --dry-run hit server")
	testutil.Require(t, strings.Contains(previewOut, "/users/usr_42?note=-h"), "previewOut = %s", previewOut)
}

func TestSnipValidationAndErrorRedactionContract(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("MYCTL_CONFIG_DIR", cfgDir)

	for _, invalidArgs := range [][]string{
		{"snip", "set", "list", "users list"},
		{"snip", "set", "-bad", "users list"},
		{"snip", "set", "loop", "snip other"},
		{"snip", "set", "self-bin", "myctl users list"},
		{"snip", "set", "group-only", "users"},
		{"snip", "set", "unknown-cmd", "missing-group list"},
		{"snip", "set", "gap-placeholder", "users update --id $2"},
		{"snip", "set", "zero-placeholder", "users update --id $0"},
		{"snip", "set", "dash-root", "--", "--", "users", "list"},
		{"snip", "set", "dash-group", "users -- list"},
	} {
		code, _, _ := runSnipCLI(t, invalidArgs...)
		testutil.Require(t, code == runtime.ExitUsage, "args %v exit=%d, want %d", invalidArgs, code, runtime.ExitUsage)
	}

	testutil.NoError(t, os.WriteFile(filepath.Join(cfgDir, "snips.yml"), []byte("loop:\n  command: snip loop\n"), 0o600))
	code, _, _ := runSnipCLI(t, "snip", "loop")
	testutil.Require(t, code == runtime.ExitUsage, "hand-edited recursive snip exit=%d, want %d", code, runtime.ExitUsage)

	for _, format := range []string{"json", "yaml"} {
		for _, secretArgs := range [][]string{
			{"-o", format, "snip", "ghp_secret_snip_token"},
			{"-o", format, "snip", "delete", "ghp_secret_snip_token"},
			{"-o", format, "snip", "set", "bad$secret_name", "users list"},
			{"-o", format, "snip", "set", "valid-name", "users list 'unclosed_secret_token"},
		} {
			code, _, stderr := runSnipCLI(t, secretArgs...)
			testutil.Require(t, code == runtime.ExitUsage, "args %v exit=%d, want %d; stderr=%s", secretArgs, code, runtime.ExitUsage, stderr)
			var env struct {
				Error runtime.LatheError `yaml:"error"`
			}
			testutil.NoError(t, yaml.Unmarshal([]byte(stderr), &env))
			testutil.Require(t, env.Error.Code == runtime.CodeUsage, "error = %#v", env.Error)
			testutil.Require(t, !strings.Contains(stderr, "secret"), "machine error leaked user value:\n%s", stderr)
		}
	}
}
