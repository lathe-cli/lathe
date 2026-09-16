package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_DryRunPrintsResolvedRequestWithoutSending(t *testing.T) {
	isolateRuntime(t)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		t.Fatalf("dry-run sent request: %s %s", r.Method, r.URL.String())
	}))
	defer srv.Close()

	root := newExecutionRoot("raw")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Users",
		Use:     "create-user",
		Method:  "POST",
		PathTpl: "/users/{id}",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true},
			{Name: "limit", Flag: "limit", In: InQuery, GoType: "int64"},
			{Name: "opaque", Flag: "opaque", In: InQuery, GoType: "string", Format: "password"},
			{Name: "value", Flag: "value", In: InBody, GoType: "string", Format: "password"},
			{Name: "values", Flag: "values", In: InBody, GoType: "[]string"},
			{Name: "page_token", Flag: "page-token", In: InQuery, GoType: "string"},
			{Name: "key", Flag: "key", In: InQuery, GoType: "string"},
			{Name: "Authorization", Flag: "authorization", In: InHeader, GoType: "string"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json", Schema: &SchemaSpec{
			Type: "object",
			Properties: map[string]*SchemaSpec{
				"values": {Type: "array", Items: &SchemaSpec{Type: "string", Format: "password"}},
			},
		}},
		Output: OutputHints{
			ListPath:          "data.items",
			DefaultColumns:    []string{"id", "name"},
			ResponseMediaType: "application/vnd.demo+json",
		},
		Security: &SecurityHint{Scopes: []string{"users:write"}},
	}})
	root.SetArgs([]string{
		"demo", "users", "create-user",
		"--hostname", srv.URL,
		"--id", "u 1",
		"--limit", "5",
		"--opaque", "dry-run-query-secret",
		"--value", "dry-run-body-secret",
		"--values", "dry-run-array-secret,another-array-secret",
		"--page-token", "cursor-1",
		"--key", "dry-run-key-secret",
		"--authorization", "Bearer secret",
		"--set", "name=alice",
		"--set", "password=hunter2",
		"--set", "envVars[0].key=MANUAL_DRY_RUN",
		"--set", "envVars[0].value=some-secret",
		"--dry-run",
	})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, hits == 0, "dry-run sent %d requests", hits)

	var out struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    map[string]any    `json:"body"`
		Auth    struct {
			Required bool `json:"required"`
			Public   bool `json:"public"`
		} `json:"auth"`
		Output struct {
			ListPath          string   `json:"list_path"`
			DefaultColumns    []string `json:"default_columns"`
			ResponseMediaType string   `json:"response_media_type"`
		} `json:"output"`
	}
	testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
	testutil.Require(t, out.Method == "POST", "method = %q, want POST", out.Method)
	testutil.Require(t, out.URL == srv.URL+"/users/u%201?key=%2A%2A%2A&limit=5&opaque=%2A%2A%2A&page_token=cursor-1", "url = %q", out.URL)
	if strings.Contains(stdout.String(), "dry-run-query-secret") || strings.Contains(stdout.String(), "dry-run-key-secret") || strings.Contains(stdout.String(), "dry-run-body-secret") || strings.Contains(stdout.String(), "dry-run-array-secret") || strings.Contains(stdout.String(), "another-array-secret") {
		t.Fatalf("dry-run leaked credential: %s", stdout.String())
	}
	testutil.Require(t, out.Headers["Authorization"] == "***", "authorization header = %q", out.Headers["Authorization"])
	testutil.Require(t, out.Headers["Content-Type"] == "application/json", "content-type = %q", out.Headers["Content-Type"])
	testutil.Require(t, out.Headers["Accept"] == "application/vnd.demo+json", "accept = %q", out.Headers["Accept"])
	testutil.Require(t, out.Body["name"] == "alice" && out.Body["password"] == "***", "body = %#v", out.Body)
	testutil.Require(t, out.Body["value"] == "***", "sensitive body field = %#v", out.Body["value"])
	testutil.Require(t, out.Body["values"] == "***", "sensitive body array = %#v", out.Body["values"])
	envVars, ok := out.Body["envVars"].([]any)
	testutil.Require(t, ok && len(envVars) == 1, "envVars = %#v", out.Body["envVars"])
	envVar, ok := envVars[0].(map[string]any)
	testutil.Require(t, ok, "envVar = %#v", envVars[0])
	testutil.Require(t, envVar["key"] == "MANUAL_DRY_RUN" && envVar["value"] == "***", "envVar = %#v", envVar)
	testutil.Require(t, out.Auth.Required && !out.Auth.Public, "auth = %+v", out.Auth)
	testutil.Require(t, out.Output.ListPath == "data.items" && out.Output.ResponseMediaType == "application/vnd.demo+json" && reflect.DeepEqual(out.Output.DefaultColumns, []string{"id", "name"}), "output = %+v", out.Output)
}
