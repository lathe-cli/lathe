package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/config"
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

func TestDryRun_ParameterSerializationMatchesWire(t *testing.T) {
	isolateRuntime(t)
	hits := 0
	var gotPath, gotQuery, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	hosts, err := config.LoadHosts()
	testutil.NoError(t, err)
	hosts.Set(srv.URL, config.HostEntry{AuthType: "apikey", APIKey: "sid=s", APIKeyHeader: "Cookie"})
	testutil.NoError(t, hosts.Save())

	spec := CommandSpec{
		Group: "Users", Use: "list", Method: "GET", PathTpl: "/users",
		Security: &SecurityHint{Public: true},
		Params: []ParamSpec{
			{Name: "roles", Flag: "roles", In: InQuery, GoType: "[]string", Style: "spaceDelimited"},
			{Name: "q", Flag: "q", In: InQuery, GoType: "string", AllowReserved: true},
			{Name: "tenant", Flag: "tenant", In: InCookie, GoType: "string"},
			{Name: "token", Flag: "token", In: InCookie, GoType: "string"},
		},
	}
	args := []string{"demo", "users", "list", "--hostname", srv.URL, "--roles", "admin,owner", "--q", "a/b:c?d#e&f=g+h", "--tenant", "acme", "--token", "sekret"}

	root := newExecutionRoot("raw")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{spec})
	root.SetArgs(args)
	testutil.NoError(t, root.Execute())
	testutil.Require(t, hits == 1, "live hits = %d", hits)
	testutil.Check(t, gotCookie == "sid=s; tenant=acme; token=sekret", "cookie = %q", gotCookie)

	var stdout bytes.Buffer
	preview := newExecutionRoot("raw")
	preview.SetOut(&stdout)
	preview.SetErr(io.Discard)
	mustBuild(t, preview, "demo", []CommandSpec{spec})
	preview.SetArgs(append(append([]string{}, args...), "--dry-run"))
	testutil.NoError(t, preview.Execute())
	testutil.Require(t, hits == 1, "dry-run sent a request, hits = %d", hits)

	var out struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
	parsed, err := url.Parse(out.URL)
	testutil.NoError(t, err)
	testutil.Check(t, parsed.EscapedPath() == gotPath, "dry-run path = %q, wire = %q", parsed.EscapedPath(), gotPath)
	testutil.Check(t, parsed.RawQuery == gotQuery, "dry-run query = %q, wire = %q", parsed.RawQuery, gotQuery)
	testutil.Check(t, gotQuery == "q=a/b:c?d%23e%26f%3Dg%2Bh&roles=admin%20owner", "query = %q", gotQuery)
	testutil.Check(t, out.Headers["Cookie"] == "sid=***; tenant=acme; token=***", "dry-run cookie = %q", out.Headers["Cookie"])
	testutil.Check(t, !strings.Contains(stdout.String(), "sekret") && !strings.Contains(stdout.String(), "sid=s"), "dry-run leaked cookie: %s", stdout.String())
}

func TestDryRun_RedactsAuthCookieSharingPublicName(t *testing.T) {
	isolateRuntime(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	hosts, err := config.LoadHosts()
	testutil.NoError(t, err)
	hosts.Set(srv.URL, config.HostEntry{AuthType: "apikey", APIKey: "tenant=SECRET", APIKeyHeader: "Cookie"})
	testutil.NoError(t, hosts.Save())

	spec := CommandSpec{
		Group: "Users", Use: "list", Method: "GET", PathTpl: "/users",
		Security: &SecurityHint{Public: true},
		Params: []ParamSpec{
			{Name: "tenant", Flag: "tenant", In: InCookie, GoType: "string"},
			{Name: "csrf_token", Flag: "csrf-token", In: InCookie, GoType: "string"},
			{Name: "session", Flag: "session", In: InCookie, GoType: "string"},
			{Name: "sid", Flag: "sid", In: InCookie, GoType: "string"},
			{Name: "color", Flag: "color", In: InCookie, GoType: "string"},
		},
	}
	var stdout bytes.Buffer
	root := newExecutionRoot("raw")
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{spec})
	root.SetArgs([]string{"demo", "users", "list", "--hostname", srv.URL, "--dry-run", "--tenant", "acme", "--csrf-token", "csrf-secret-value", "--session", "session-secret-value", "--sid", "sid-secret-value", "--color", "blue"})
	testutil.NoError(t, root.Execute())

	var out struct {
		Headers map[string]string `json:"headers"`
	}
	testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
	testutil.Check(t, out.Headers["Cookie"] == "tenant=***; tenant=acme; csrf_token=***; session=***; sid=***; color=blue", "cookie = %q", out.Headers["Cookie"])
	for _, secret := range []string{"SECRET", "csrf-secret-value", "session-secret-value", "sid-secret-value"} {
		testutil.Check(t, !strings.Contains(stdout.String(), secret), "dry-run leaked %s: %s", secret, stdout.String())
	}
}
