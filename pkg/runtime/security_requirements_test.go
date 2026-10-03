package runtime

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestSecurityRequirements_BeforeSend(t *testing.T) {
	const (
		bearerSecret = "bearer-secret-value"
		apiKeySecret = "apikey-secret-value"
		basicSecret  = "basic-secret-value"
	)
	var called bool
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		header = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	orSpec := CommandSpec{
		Group: "Users", Use: "list", Method: "GET", PathTpl: "/users",
		Security: &SecurityHint{Requirements: []SecurityRequirement{
			{Schemes: []SecurityScheme{{Name: "bearerAuth", Type: "http", Scheme: "bearer"}}},
			{Schemes: []SecurityScheme{{Name: "apiKeyAuth", Type: "apiKey", In: "header", Param: "X-API-Key"}}},
		}},
	}
	andSpec := CommandSpec{
		Group: "Audit", Use: "export", Method: "GET", PathTpl: "/audit/export",
		Security: &SecurityHint{Requirements: []SecurityRequirement{{
			Schemes: []SecurityScheme{
				{Name: "bearerAuth", Type: "http", Scheme: "bearer"},
				{Name: "tenantKey", Type: "apiKey", In: "header", Param: "X-Tenant-Key"},
			},
		}}},
	}
	optionalSpec := CommandSpec{
		Group: "Health", Use: "check", Method: "GET", PathTpl: "/health",
		Security: &SecurityHint{
			Public: true,
			Requirements: []SecurityRequirement{
				{},
				{Schemes: []SecurityScheme{{Name: "bearerAuth", Type: "http", Scheme: "bearer"}}},
			},
		},
	}

	store := func(t *testing.T, entry config.HostEntry) {
		t.Helper()
		hosts, err := config.LoadHosts()
		testutil.Require(t, err == nil, "LoadHosts: %v", err)
		hosts.Set(srv.URL, entry)
		testutil.NoError(t, hosts.Save())
	}
	run := func(t *testing.T, spec CommandSpec, args ...string) (string, error) {
		t.Helper()
		called = false
		header = nil
		root := newExecutionRoot("json")
		root.SilenceErrors = true
		root.SilenceUsage = true
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		mustBuild(t, root, "demo", []CommandSpec{spec})
		root.SetArgs(append([]string{"--hostname", srv.URL}, args...))
		err := root.Execute()
		if err != nil {
			FormatError(err, "json", &out)
		}
		return out.String(), err
	}

	t.Run("bearer satisfies or", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "bearer", OAuthToken: bearerSecret})
		out, err := run(t, orSpec, "demo", "users", "list")
		testutil.Require(t, err == nil, "execute: %v\n%s", err, out)
		testutil.Require(t, called, "server was not called")
		testutil.Require(t, header.Get("Authorization") == "Bearer "+bearerSecret, "authorization = %q", header.Get("Authorization"))
	})

	t.Run("apikey satisfies or", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "apikey", APIKey: apiKeySecret, APIKeyHeader: "x-api-key"})
		out, err := run(t, orSpec, "demo", "users", "list")
		testutil.Require(t, err == nil, "execute: %v\n%s", err, out)
		testutil.Require(t, called, "server was not called")
		testutil.Require(t, header.Get("x-api-key") == apiKeySecret, "api key header = %q", header.Get("x-api-key"))
	})

	t.Run("basic satisfies neither or scheme", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "basic", BasicUser: "user", BasicPassword: basicSecret})
		out, err := run(t, orSpec, "demo", "users", "list")
		testutil.Require(t, !called, "server was called")
		var le *LatheError
		testutil.Require(t, errors.As(err, &le), "error = %v", err)
		testutil.Require(t, le.Code == CodeNotAuthenticated && le.ExitCode == ExitNotAuthenticated, "error = %+v", le)
		testutil.Require(t, strings.Contains(out, "bearerAuth (http bearer)") && strings.Contains(out, "apiKeyAuth (apiKey header X-API-Key)"), "error json = %s", out)
		testutil.Require(t, !strings.Contains(out, basicSecret), "error json contains secret: %s", out)
	})

	t.Run("and requires both schemes", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "bearer", OAuthToken: bearerSecret})
		out, err := run(t, andSpec, "demo", "audit", "export")
		testutil.Require(t, !called, "server was called")
		var le *LatheError
		testutil.Require(t, errors.As(err, &le), "error = %v", err)
		testutil.Require(t, le.Code == CodeNotAuthenticated, "code = %s", le.Code)
		testutil.Require(t, strings.Contains(le.Detail, "bearerAuth (http bearer) + tenantKey (apiKey header X-Tenant-Key)"), "detail = %q", le.Detail)
		testutil.Require(t, !strings.Contains(out, bearerSecret), "error json contains secret: %s", out)
	})

	t.Run("request parameters can supply schemes", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "bearer", OAuthToken: bearerSecret})
		withHeader := andSpec
		withHeader.Params = []ParamSpec{{Name: "X-Tenant-Key", Flag: "x-tenant-key", In: InHeader, GoType: "string"}}
		out, err := run(t, withHeader, "demo", "audit", "export", "--x-tenant-key", "tenant")
		testutil.Require(t, err == nil && called, "execute: %v\n%s", err, out)
		testutil.Require(t, header.Get("X-Tenant-Key") == "tenant", "tenant header = %q", header.Get("X-Tenant-Key"))

		querySpec := CommandSpec{
			Group: "Users", Use: "list", Method: "GET", PathTpl: "/users",
			Params: []ParamSpec{{Name: "api_key", Flag: "api-key", In: InQuery, GoType: "string"}},
			Security: &SecurityHint{Requirements: []SecurityRequirement{
				{Schemes: []SecurityScheme{{Name: "queryKey", Type: "apiKey", In: "query", Param: "api_key"}}},
			}},
		}
		_, err = run(t, querySpec, "demo", "users", "list")
		testutil.Require(t, !called && err != nil, "missing query key was sent")
		out, err = run(t, querySpec, "demo", "users", "list", "--api-key", "k")
		testutil.Require(t, err == nil && called, "execute: %v\n%s", err, out)
	})

	t.Run("apikey in authorization header satisfies bearer", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "apikey", APIKey: "token " + apiKeySecret, APIKeyHeader: "Authorization"})
		bearerOnly := orSpec
		bearerOnly.Security = &SecurityHint{Requirements: orSpec.Security.Requirements[:1]}
		out, err := run(t, bearerOnly, "demo", "users", "list")
		testutil.Require(t, err == nil && called, "execute: %v\n%s", err, out)
		testutil.Require(t, header.Get("Authorization") == "token "+apiKeySecret, "authorization = %q", header.Get("Authorization"))
	})

	t.Run("anonymous alternative needs no host", func(t *testing.T) {
		isolateRuntime(t)
		out, err := run(t, optionalSpec, "demo", "health", "check")
		testutil.Require(t, err == nil, "execute: %v\n%s", err, out)
		testutil.Require(t, called, "server was not called")
		testutil.Require(t, header.Get("Authorization") == "", "authorization = %q", header.Get("Authorization"))
	})

	t.Run("workflow step uses the same check", func(t *testing.T) {
		isolateRuntime(t)
		store(t, config.HostEntry{AuthType: "bearer", OAuthToken: bearerSecret})
		called = false
		root := newWorkflowRoot(io.Discard)
		root.SilenceErrors = true
		root.SilenceUsage = true
		testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
			Use:   "export-audit",
			Steps: []WorkflowStepSpec{{ID: "export", Operation: andSpec}},
		}}))
		root.SetArgs([]string{"--hostname", srv.URL, "export-audit"})
		err := root.Execute()
		testutil.Require(t, !called, "server was called")
		var le *LatheError
		testutil.Require(t, errors.As(err, &le) && le.Code == CodeNotAuthenticated, "error = %v", err)
		testutil.Require(t, strings.Contains(le.Detail, "tenantKey"), "detail = %q", le.Detail)
	})
}
