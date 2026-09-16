package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestLoad_FullSpec(t *testing.T) {
	data := []byte(`
cli:
  name: demo
  short: "demo CLI"
contexts:
  workspace:
    env: DEMO_WORKSPACE_ID
auth:
  default_type: apikey
  api_key_header: X-Auth-Token
  login:
    type: oauth_device
    start_path: /auth/cli/start
    token_path: /auth/cli/token
    refresh_path: /auth/cli/refresh
    start_request:
      client_id: demo-cli
      device_label: ${device_label}
    poll_request:
      client_id: demo-cli
      device_code: ${device_code}
    poll_response:
      access_token: token
      status: state
      error: failure.code
      contexts:
        workspace: account.workspace_id
  validate:
    method: POST
    path: /whoami
    assert:
      field: user.id
      non_empty: true
    display:
      username_field: user.name
      fallback_field: uid
`)
	m, err := Load(data)
	testutil.Require(t, err == nil, "Load: %v", err)
	testutil.Check(t, m.CLI.Name == "demo" && m.CLI.Short == "demo CLI", "unexpected CLI: %+v", m.CLI)
	testutil.Require(t, m.Auth.Validate != nil, "expected Auth.Validate non-nil")
	testutil.Require(t, m.Auth.Login != nil, "expected Auth.Login non-nil")
	testutil.Check(t, m.Auth.DefaultType == "apikey" && m.Auth.APIKeyHeader == "X-Auth-Token", "unexpected auth defaults: %+v", m.Auth)
	testutil.Check(t, m.Auth.Login.Type == AuthLoginOAuthDevice && m.Auth.Login.StartPath == "/auth/cli/start" && m.Auth.Login.TokenPath == "/auth/cli/token" && m.Auth.Login.RefreshPath == "/auth/cli/refresh", "unexpected AuthLogin: %+v", m.Auth.Login)
	testutil.Check(t, m.Auth.Login.StartRequest["client_id"] == "demo-cli" && m.Auth.Login.StartRequest["device_label"] == "${device_label}", "unexpected start request: %+v", m.Auth.Login.StartRequest)
	testutil.Check(t, m.Auth.Login.PollRequest["client_id"] == "demo-cli" && m.Auth.Login.PollRequest["device_code"] == "${device_code}", "unexpected poll request: %+v", m.Auth.Login.PollRequest)
	testutil.Check(t, m.Auth.Login.PollResponse.AccessToken == "token" && m.Auth.Login.PollResponse.Status == "state" && m.Auth.Login.PollResponse.Error == "failure.code", "unexpected poll response: %+v", m.Auth.Login.PollResponse)
	testutil.Check(t, m.Contexts["workspace"].Env == "DEMO_WORKSPACE_ID" && m.Auth.Login.PollResponse.Contexts["workspace"] == "account.workspace_id", "unexpected contexts: manifest=%+v poll=%+v", m.Contexts, m.Auth.Login.PollResponse.Contexts)
	testutil.Check(t, m.Auth.Validate.Method == "POST" && m.Auth.Validate.Path == "/whoami", "unexpected AuthValidate: %+v", m.Auth.Validate)
	testutil.Check(t, m.Auth.Validate.Assert != nil && m.Auth.Validate.Assert.Field == "user.id" && m.Auth.Validate.Assert.NonEmpty, "unexpected AuthValidate.Assert: %+v", m.Auth.Validate.Assert)
	testutil.Check(t, m.Auth.Validate.Display.UsernameField == "user.name", "unexpected UsernameField: %q", m.Auth.Validate.Display.UsernameField)
	testutil.Check(t, m.Auth.Validate.Display.FallbackField == "uid", "unexpected FallbackField: %q", m.Auth.Validate.Display.FallbackField)
}

func TestLoadRejectsUnsafeContextName(t *testing.T) {
	_, err := Load([]byte("cli:\n  name: demo\ncontexts:\n  \"workspace` **INJECT**\": {}\n"))
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "context name"), "error = %v, want invalid context name", err)
}

func TestLoadRejectsCaseInsensitiveDuplicateContextEnv(t *testing.T) {
	_, err := Load([]byte("cli:\n  name: demo\ncontexts:\n  workspace:\n    env: WORKSPACE_ID\n  organization:\n    env: workspace_id\n"))
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "same environment variable"), "error = %v, want duplicate environment variable", err)
}

func TestLoadRejectsContextEnvReservedByCLI(t *testing.T) {
	for _, env := range []string{"demo_host", "DEMO_CONFIG_DIR"} {
		_, err := Load([]byte("cli:\n  name: demo\ncontexts:\n  workspace:\n    env: " + env + "\n"))
		testutil.Check(t, err != nil && strings.Contains(err.Error(), "reserved"), "env %q error = %v, want reserved environment variable", env, err)
	}
}

func TestLoad_NoAuthValidate(t *testing.T) {
	data := []byte(`
cli:
  name: demo
`)
	m, err := Load(data)
	testutil.Require(t, err == nil, "Load: %v", err)
	testutil.Check(t, m.Auth.Validate == nil, "expected Auth.Validate nil, got %+v", m.Auth.Validate)
}

// Empty method is preserved by Load; the default-to-GET is applied at
// validateToken() call time, not at parse time.
func TestLoad_PreservesEmptyMethod(t *testing.T) {
	data := []byte(`
cli:
  name: demo
auth:
  validate:
    path: /whoami
    display:
      username_field: username
`)
	m, err := Load(data)
	testutil.Require(t, err == nil, "Load: %v", err)
	testutil.Require(t, m.Auth.Validate != nil, "expected Auth.Validate non-nil")
	testutil.Check(t, m.Auth.Validate.Method == "", "expected empty method, got %q", m.Auth.Validate.Method)
}

func TestLoad_Malformed(t *testing.T) {
	_, err := Load([]byte("this: is: not: yaml"))
	testutil.Require(t, err != nil, "expected error on malformed YAML")
}

func TestLoad_RequiresName(t *testing.T) {
	_, err := Load([]byte(`cli: {}`))
	testutil.Require(t, err != nil, "expected error when cli.name is missing")
}

func TestLoad_DerivesIdentityDefaults(t *testing.T) {
	m, err := Load([]byte(`cli: {name: foobar}`))
	testutil.Require(t, err == nil, "Load: %v", err)
	if got, want := m.CLI.ConfigDir, "foobar"; got != want {
		t.Errorf("ConfigDir: got %q, want %q", got, want)
	}
	if got, want := m.CLI.ConfigDirEnv, "FOOBAR_CONFIG_DIR"; got != want {
		t.Errorf("ConfigDirEnv: got %q, want %q", got, want)
	}
	if got, want := m.CLI.HostEnv, "FOOBAR_HOST"; got != want {
		t.Errorf("HostEnv: got %q, want %q", got, want)
	}
	if got, want := m.CLI.CommandPath, CommandPathAuto; got != want {
		t.Errorf("CommandPath: got %q, want %q", got, want)
	}
}

func TestLoad_PreservesExplicitIdentity(t *testing.T) {
	m, err := Load([]byte(`
cli:
  name: foo
  config_dir: legacy
  config_dir_env: LEGACY_CONFIG
  host_env: LEGACY_HOST
`))
	testutil.Require(t, err == nil, "Load: %v", err)
	testutil.Check(t, m.CLI.ConfigDir == "legacy" && m.CLI.ConfigDirEnv == "LEGACY_CONFIG" && m.CLI.HostEnv == "LEGACY_HOST", "explicit identity overridden: %+v", m.CLI)
}

func TestLoad_CommandPath(t *testing.T) {
	m, err := Load([]byte(`
cli:
  name: foo
  command_path: Namespaced
`))
	testutil.Require(t, err == nil, "Load: %v", err)
	if got, want := m.CLI.CommandPath, CommandPathNamespaced; got != want {
		t.Fatalf("CommandPath: got %q, want %q", got, want)
	}

	_, err = Load([]byte(`
cli:
  name: foo
  command_path: short
`))
	testutil.Require(t, err != nil, "expected invalid command_path error")
}

func TestLoad_WorkflowRejectsDuplicateInputs(t *testing.T) {
	for _, tc := range []struct{ name, second, want string }{
		{"name", "app_id", "input name"},
		{"flag", "app.id", "input flag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(fmt.Sprintf(`
cli:
  name: demo
workflow:
  commands:
    - use: doctor
      inputs:
        - name: app_id
        - name: %s
      steps:
        - id: health
          uses: acme.getHealth
`, tc.second)))
			testutil.Require(t, err != nil && strings.Contains(err.Error(), tc.want), "error = %v, want %q", err, tc.want)
		})
	}
}

func TestLoad_AuthLoginValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "unsupported type",
			yaml: `
  login:
    type: github
    start_path: /start
    token_path: /token
`,
		},
		{
			name: "missing token path",
			yaml: `
  login:
    type: oauth_device
    start_path: /start
`,
		},
		{
			name: "relative path",
			yaml: `
  login:
    type: oauth_device
    start_path: start
    token_path: /token
`,
		},
		{
			name: "unsupported default auth type",
			yaml: `
  default_type: digest
`,
		},
		{
			name: "oauth default without login block",
			yaml: `
  default_type: oauth
`,
		},
		{
			name: "empty assertion",
			yaml: `
  validate:
    path: /whoami
    assert: {}
`,
		},
		{
			name: "unsupported request placeholder",
			yaml: `
  login:
    type: oauth_device
    start_path: /start
    token_path: /token
    start_request:
      client_id: ${unknown}
`,
		},
		{
			name: "unknown login context",
			yaml: `
  login:
    type: oauth_device
    start_path: /start
    token_path: /token
    poll_response:
      contexts:
        workspace: account.workspace_id
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load([]byte("cli:\n  name: demo\nauth:\n" + tc.yaml)); err == nil {
				t.Fatal("Load succeeded, want error")
			}
		})
	}
}

func TestBindActive_Panics(t *testing.T) {
	boundMu.Lock()
	bound = nil
	boundMu.Unlock()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected Active() to panic before Bind()")
		}
	}()
	_ = Active()
}

func TestBindActive_RoundTrip(t *testing.T) {
	m := &Manifest{CLI: CLIInfo{Name: "x", ConfigDir: "x", ConfigDirEnv: "X_CONFIG_DIR", HostEnv: "X_HOST"}}
	Bind(m)
	t.Cleanup(func() {
		boundMu.Lock()
		bound = nil
		boundMu.Unlock()
	})
	testutil.Require(t, Active() == m, "Active() did not return the bound manifest")
}

func TestLoadManifest_WorkflowConditions(t *testing.T) {
	t.Run("accepts scalar values in any form", func(t *testing.T) {
		m := mustLoadWorkflowManifest(t, `
      steps:
        - id: probe
          uses: console.Apps_Get
          when:
            - value: ${input.kind}
              operator: in
              values: [gpu, 404, "cpu", ""]
`)
		cond := m.Workflow.Commands[0].Steps[0].When
		testutil.Require(t, len(cond) == 1, "conditions = %#v", cond)
		want := WorkflowConditionValues{"gpu", "404", "cpu", ""}
		testutil.Require(t, reflect.DeepEqual(cond[0].Values, want), "values = %#v, want %#v", cond[0].Values, want)
	})

	for name, when := range map[string]string{
		"missing operator": `
            - value: ${input.kind}
              values: [gpu]`,
		"unknown operator": `
            - value: ${input.kind}
              operator: matches
              values: [gpu]`,
		"empty values": `
            - value: ${input.kind}
              operator: in
              values: []`,
		"missing value": `
            - operator: in
              values: [gpu]`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := Load([]byte(workflowManifestYAML(`
      steps:
        - id: probe
          uses: console.Apps_Get
          when:` + when + "\n")))
			testutil.Require(t, err != nil, "expected an error")
		})
	}
}

func mustLoadWorkflowManifest(t *testing.T, steps string) *Manifest {
	t.Helper()
	m, err := Load([]byte(workflowManifestYAML(steps)))
	testutil.Require(t, err == nil, "LoadManifest: %v", err)
	return m
}

func workflowManifestYAML(steps string) string {
	return `cli:
  name: myctl
workflow:
  version: 1
  commands:
    - use: deploy
      inputs:
        - name: kind
` + steps
}

// Condition values must be normalized the way the runtime stringifies values,
// otherwise `values: [1.0]` never matches a float64 input of 1.
func TestLoadManifest_WorkflowConditionValuesMatchRuntimeFormatting(t *testing.T) {
	m := mustLoadWorkflowManifest(t, `
      steps:
        - id: probe
          uses: console.Apps_Get
          when:
            - value: ${input.kind}
              operator: in
              values: [1.0, 1.50, 404, 2.5, gpu, "1.0", true, .nan, .inf, -.inf, null]
`)
	got := m.Workflow.Commands[0].Steps[0].When[0].Values
	want := WorkflowConditionValues{"1", "1.5", "404", "2.5", "gpu", "1.0", "true", "NaN", "+Inf", "-Inf", ""}
	testutil.Require(t, reflect.DeepEqual(got, want), "values = %#v, want %#v", got, want)
}

func TestLoadManifest_WorkflowConditionPreservesValueWhitespace(t *testing.T) {
	m := mustLoadWorkflowManifest(t, `
      steps:
        - id: probe
          uses: console.Apps_Get
          when:
            - value: " ${input.kind} "
              operator: in
              values: [" gpu "]
`)
	if got := m.Workflow.Commands[0].Steps[0].When[0].Value; got != " ${input.kind} " {
		t.Fatalf("value = %q, want surrounding spaces preserved", got)
	}
}
