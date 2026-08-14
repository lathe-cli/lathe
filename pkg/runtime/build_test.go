package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newRootWithModuleGroup() *cobra.Command {
	root := &cobra.Command{Use: "myctl"}
	root.AddGroup(&cobra.Group{ID: ModuleGroupID, Title: "Modules"})
	return root
}

func mustBuild(t *testing.T, root *cobra.Command, service string, specs []CommandSpec) {
	t.Helper()
	if err := Build(root, service, specs); err != nil {
		t.Fatalf("Build(%q): %v", service, err)
	}
}

func TestBuild_RejectsExistingRootCommandConflict(t *testing.T) {
	root := newRootWithModuleGroup()
	root.AddCommand(&cobra.Command{Use: "auth"})

	err := Build(root, "auth", nil)
	if err == nil || !strings.Contains(err.Error(), `module command "auth" conflicts`) {
		t.Fatalf("expected root conflict error, got %v", err)
	}
	if len(root.Commands()) != 1 {
		t.Fatalf("conflicting module must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}
}

func TestBuild_PopulatesGroupAndOpTree(t *testing.T) {
	specs := []CommandSpec{
		{
			Group:   "Users",
			Use:     "get-user",
			Short:   "Get a user",
			Method:  "GET",
			PathTpl: "/users/{id}",
			Params: []ParamSpec{
				{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: "User id"},
				{Name: "limit", Flag: "limit", In: InQuery, GoType: "int64", Help: "Page size"},
			},
		},
		{
			Group:   "Items",
			Use:     "list-items",
			Short:   "List items",
			Method:  "GET",
			PathTpl: "/items",
			Params: []ParamSpec{
				{Name: "verbose", Flag: "verbose", In: InQuery, GoType: "bool", Help: "Verbose output"},
			},
		},
	}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	usersGroup := mustFindChild(t, svc, "users")
	itemsGroup := mustFindChild(t, svc, "items")

	if len(usersGroup.Commands()) != 1 || usersGroup.Commands()[0].Use != "get-user" {
		t.Errorf("users group commands = %v, want [get-user]", cmdNames(usersGroup.Commands()))
	}
	if len(itemsGroup.Commands()) != 1 || itemsGroup.Commands()[0].Use != "list-items" {
		t.Errorf("items group commands = %v, want [list-items]", cmdNames(itemsGroup.Commands()))
	}

	getUser := usersGroup.Commands()[0]
	if f := getUser.Flag("id"); f == nil {
		t.Errorf("get-user missing --id flag")
	} else if !isRequiredFlag(f.Annotations) {
		t.Errorf("get-user --id flag is not marked required")
	}
	if f := getUser.Flag("limit"); f == nil {
		t.Errorf("get-user missing --limit flag")
	} else if f.Value.Type() != "int64" {
		t.Errorf("get-user --limit type = %q, want int64", f.Value.Type())
	}

	listItems := itemsGroup.Commands()[0]
	if f := listItems.Flag("verbose"); f == nil {
		t.Errorf("list-items missing --verbose flag")
	} else if f.Value.Type() != "bool" {
		t.Errorf("list-items --verbose type = %q, want bool", f.Value.Type())
	}
}

func TestAssertSchema_Match(t *testing.T) {
	if err := AssertSchema(SchemaVersion); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertSchema_Mismatch(t *testing.T) {
	err := AssertSchema(SchemaVersion + 999)
	if err == nil {
		t.Fatal("expected error on schema mismatch")
	}
	if !strings.Contains(err.Error(), "re-run codegen") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuild_EmptySpecsMountsEmptyService(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", nil)

	svc := mustFindChild(t, root, "demo")
	if len(svc.Commands()) != 0 {
		t.Errorf("empty specs should yield no subcommands under demo; got %v", cmdNames(svc.Commands()))
	}
}

func TestBuildFlat_PopulatesRootGroupTree(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}}

	root := newRootWithModuleGroup()
	if err := BuildFlat(root, "demo", specs); err != nil {
		t.Fatalf("BuildFlat: %v", err)
	}

	users := mustFindChild(t, root, "users")
	getUser := mustFindChild(t, users, "get-user")
	entry, ok := catalogCommandFromAnnotation(getUser, []string{"users", "get-user"})
	if !ok {
		t.Fatal("missing catalog annotation")
	}
	if !reflect.DeepEqual(entry.Path, []string{"users", "get-user"}) {
		t.Fatalf("path = %#v", entry.Path)
	}
	if entry.Service != "demo" {
		t.Fatalf("service = %q, want demo", entry.Service)
	}
}

func TestBuildFlat_RejectsRootCommandConflict(t *testing.T) {
	root := newRootWithModuleGroup()
	root.AddCommand(&cobra.Command{Use: "search"})

	err := BuildFlat(root, "demo", []CommandSpec{{Group: "Search", Use: "query"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected conflict error, got %v", err)
	}
	if len(mustFindChild(t, root, "search").Commands()) != 0 {
		t.Fatal("conflicting generated group should not be attached")
	}
}

func TestBuildFlat_RejectsGeneratedGroupNameConflict(t *testing.T) {
	root := newRootWithModuleGroup()

	err := BuildFlat(root, "demo", []CommandSpec{
		{Group: "Users", Use: "list", Method: "GET", PathTpl: "/users"},
		{Group: "Users API", Use: "get", Method: "GET", PathTpl: "/users/{id}"},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected generated group conflict error, got %v", err)
	}
	if len(root.Commands()) != 0 {
		t.Fatalf("conflicting generated groups should not be attached, got %v", cmdNames(root.Commands()))
	}
}

func TestBuild_RejectsAliasThatShadowsCanonicalCommand(t *testing.T) {
	root := newRootWithModuleGroup()
	err := Build(root, "demo", []CommandSpec{
		{Group: "Users", Use: "get-user", Method: "GET", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "remove-user", Aliases: []string{"get-user"}, Method: "DELETE", PathTpl: "/users/{id}"},
	})
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("Build error = %v, want alias conflict", err)
	}

	root = newRootWithModuleGroup()
	err = Build(root, "demo", []CommandSpec{
		{Group: "Users API", Use: "remove-user", Aliases: []string{"get-user"}, Method: "DELETE", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "get-user", Method: "GET", PathTpl: "/users/{id}"},
	})
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("Build error = %v, want normalized group alias conflict", err)
	}
}

func TestBuild_BodyFlagsAttachedWhenHasBody(t *testing.T) {
	specs := []CommandSpec{{
		Group:       "Users",
		Use:         "create-user",
		Method:      "POST",
		PathTpl:     "/users",
		RequestBody: &RequestBody{Required: true},
	}}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	users := mustFindChild(t, svc, "users")
	createUser := mustFindChild(t, users, "create-user")

	for _, name := range []string{"file", "set", "set-str"} {
		if createUser.Flag(name) == nil {
			t.Errorf("create-user missing --%s flag", name)
		}
	}
}

func TestBuild_SetStrSendsStringBodyFields(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		rawBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:       "Users",
		Use:         "create-user",
		Method:      "POST",
		PathTpl:     "/users",
		RequestBody: &RequestBody{Required: true},
		Security:    &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{
		"--hostname", srv.URL,
		"demo", "users", "create-user",
		"--set", "spec.replicas=3",
		"--set", "spec.enabled=true",
		"--set-str", "spec.stringReplicas=3",
		"--set-str", "spec.stringEnabled=true",
		"--set-str", "spec.csv=a,b",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	want := map[string]any{
		"spec": map[string]any{
			"replicas":       float64(3),
			"enabled":        true,
			"stringReplicas": "3",
			"stringEnabled":  "true",
			"csv":            "a,b",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestBuild_FileSendsRequestBodyMediaType(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	bodyFile := t.TempDir() + "/body.txt"
	if err := os.WriteFile(bodyFile, []byte("hello"), 0600); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:       "Exports",
		Use:         "create-export",
		Method:      "POST",
		PathTpl:     "/exports",
		RequestBody: &RequestBody{Required: true, MediaType: "text/plain"},
		Security:    &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "exports", "create-export", "--file", bodyFile})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotContentType != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", gotContentType)
	}
}

func TestBuild_NonJSONRequestBodyRequiresFile(t *testing.T) {
	for _, args := range [][]string{
		{"--set", "id=1"},
		nil,
	} {
		bindTestManifest(t, "myctl", "MYCTL_HOST")
		t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

		root := newRootWithModuleGroup()
		root.PersistentFlags().String("hostname", "", "")
		root.PersistentFlags().StringP("output", "o", "raw", "")
		mustBuild(t, root, "demo", []CommandSpec{{
			Group:       "Exports",
			Use:         "create-export",
			Method:      "POST",
			PathTpl:     "/exports",
			RequestBody: &RequestBody{Required: true, MediaType: "text/plain"},
			Security:    &SecurityHint{Public: true},
		}})
		root.SetArgs(append([]string{"--hostname", "http://127.0.0.1:1", "demo", "exports", "create-export"}, args...))

		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "requires --file") {
			t.Fatalf("Execute error = %v, want requires --file", err)
		}
	}
}

func TestBuild_DryRunPrintsResolvedRequestWithoutSending(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		t.Fatalf("dry-run sent request: %s %s", r.Method, r.URL.String())
	}))
	defer srv.Close()

	root := newRootWithModuleGroup()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Users",
		Use:     "create-user",
		Method:  "POST",
		PathTpl: "/users/{id}",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true},
			{Name: "limit", Flag: "limit", In: InQuery, GoType: "int64"},
			{Name: "opaque", Flag: "opaque", In: InQuery, GoType: "string", Format: "password"},
			{Name: "page_token", Flag: "page-token", In: InQuery, GoType: "string"},
			{Name: "key", Flag: "key", In: InQuery, GoType: "string"},
			{Name: "Authorization", Flag: "authorization", In: InHeader, GoType: "string"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
		Output: OutputHints{
			ListPath:          "data.items",
			DefaultColumns:    []string{"id", "name"},
			ResponseMediaType: "application/vnd.demo+json",
		},
		Security: &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{
		"demo", "users", "create-user",
		"--hostname", srv.URL,
		"--id", "u 1",
		"--limit", "5",
		"--opaque", "dry-run-query-secret",
		"--page-token", "cursor-1",
		"--key", "dry-run-key-secret",
		"--authorization", "Bearer secret",
		"--set", "name=alice",
		"--set", "password=hunter2",
		"--set", "envVars[0].key=MANUAL_DRY_RUN",
		"--set", "envVars[0].value=some-secret",
		"--dry-run",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if hits != 0 {
		t.Fatalf("dry-run sent %d requests", hits)
	}

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
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("dry-run JSON: %v\n%s", err, stdout.String())
	}
	if out.Method != "POST" {
		t.Fatalf("method = %q, want POST", out.Method)
	}
	if out.URL != srv.URL+"/users/u%201?key=%2A%2A%2A&limit=5&opaque=%2A%2A%2A&page_token=cursor-1" {
		t.Fatalf("url = %q", out.URL)
	}
	if strings.Contains(stdout.String(), "dry-run-query-secret") || strings.Contains(stdout.String(), "dry-run-key-secret") {
		t.Fatalf("dry-run leaked query credential: %s", stdout.String())
	}
	if out.Headers["Authorization"] != "***" {
		t.Fatalf("authorization header = %q", out.Headers["Authorization"])
	}
	if out.Headers["Content-Type"] != "application/json" {
		t.Fatalf("content-type = %q", out.Headers["Content-Type"])
	}
	if out.Headers["Accept"] != "application/vnd.demo+json" {
		t.Fatalf("accept = %q", out.Headers["Accept"])
	}
	if out.Body["name"] != "alice" || out.Body["password"] != "***" {
		t.Fatalf("body = %#v", out.Body)
	}
	envVars, ok := out.Body["envVars"].([]any)
	if !ok || len(envVars) != 1 {
		t.Fatalf("envVars = %#v", out.Body["envVars"])
	}
	envVar, ok := envVars[0].(map[string]any)
	if !ok {
		t.Fatalf("envVar = %#v", envVars[0])
	}
	if envVar["key"] != "MANUAL_DRY_RUN" || envVar["value"] != "***" {
		t.Fatalf("envVar = %#v", envVar)
	}
	if out.Auth.Required || !out.Auth.Public {
		t.Fatalf("auth = %+v", out.Auth)
	}
	if out.Output.ListPath != "data.items" || out.Output.ResponseMediaType != "application/vnd.demo+json" || !reflect.DeepEqual(out.Output.DefaultColumns, []string{"id", "name"}) {
		t.Fatalf("output = %+v", out.Output)
	}
}

func TestBuild_VariableFlagsMergeIntoEnvelope(t *testing.T) {
	root, url, recorded := newRecordingGraphQLRoot(t, createAppSpec())
	root.SetArgs([]string{"--hostname", url, "demo", "apps", "create-app", "--input-name", "demo"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rawBody, _ := recorded()
	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	if q, _ := got["query"].(string); !strings.Contains(q, "mutation createApp") {
		t.Errorf("query missing baked document: %#v", got["query"])
	}
	vars, _ := got["variables"].(map[string]any)
	input, _ := vars["input"].(map[string]any)
	if input["name"] != "demo" {
		t.Errorf("variables = %#v, want input.name=demo", got["variables"])
	}
}

func TestBuild_SensitiveVariableSafeInputModes(t *testing.T) {
	root, url, recorded := newRecordingGraphQLRoot(t, createCredentialSpec())
	t.Setenv("OPENAI_API_KEY", "sk-env")

	cmd := mustFindChild(t, mustFindChild(t, mustFindChild(t, root, "demo"), "credentials"), "create-credential")
	for _, flag := range []string{"input-api-key-env", "input-api-key-file", "input-api-key-stdin"} {
		if cmd.Flag(flag) == nil {
			t.Fatalf("missing --%s", flag)
		}
	}

	root.SetArgs([]string{"--hostname", url, "demo", "credentials", "create-credential", "--input-api-key-env", "OPENAI_API_KEY"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	body, called := recorded()
	if !called {
		t.Fatal("request was not sent")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(body), err)
	}
	vars, _ := got["variables"].(map[string]any)
	input, _ := vars["input"].(map[string]any)
	if input["apiKey"] != "sk-env" {
		t.Fatalf("apiKey = %#v, want sk-env", input["apiKey"])
	}
}

func TestBuild_RequiredVariableCanComeFromBodyInput(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		fileBody string
		wantName string
		wantErr  bool
	}{
		{
			name:     "file",
			args:     []string{"--file", "BODY_FILE"},
			fileBody: `{"input":{"name":"from-file"}}`,
			wantName: "from-file",
		},
		{
			name:     "set",
			args:     []string{"--set", "input.name=from-set"},
			wantName: "from-set",
		},
		{
			name:    "missing",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, url, recorded := newRecordingGraphQLRoot(t, createAppSpec())
			args := append([]string{"--hostname", url, "demo", "apps", "create-app"}, tc.args...)
			if tc.fileBody != "" {
				bodyFile := t.TempDir() + "/body.json"
				if err := os.WriteFile(bodyFile, []byte(tc.fileBody), 0600); err != nil {
					t.Fatalf("write body file: %v", err)
				}
				for i := range args {
					if args[i] == "BODY_FILE" {
						args[i] = bodyFile
					}
				}
			}
			root.SetArgs(args)
			err := root.Execute()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				_, called := recorded()
				if called {
					t.Fatal("request should not be sent")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			rawBody, called := recorded()
			if !called {
				t.Fatal("request was not sent")
			}
			var got map[string]any
			if err := json.Unmarshal(rawBody, &got); err != nil {
				t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
			}
			vars, _ := got["variables"].(map[string]any)
			input, _ := vars["input"].(map[string]any)
			if input["name"] != tc.wantName {
				t.Errorf("variables = %#v, want input.name=%s", got["variables"], tc.wantName)
			}
		})
	}
}

func createCredentialSpec() CommandSpec {
	return CommandSpec{
		Group:   "Credentials",
		Use:     "create-credential",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "input.apiKey", Flag: "input-api-key", In: InVariable, GoType: "string", Required: true, Help: "API key"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation createCredential($input: CredentialInput!) { createCredential(input: $input) { id } }","variables":{"input":{}}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}
}

func createAppSpec() CommandSpec {
	return CommandSpec{
		Group:   "Apps",
		Use:     "create-app",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "input.name", Flag: "input-name", In: InVariable, GoType: "string", Required: true, Help: "name"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation createApp($input: CreateAppInput!) { createApp(input: $input) { id } }","variables":{"input":{}}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}
}

func newRecordingGraphQLRoot(t *testing.T, spec CommandSpec) (*cobra.Command, string, func() ([]byte, bool)) {
	t.Helper()
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var rawBody []byte
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", []CommandSpec{spec})
	return root, srv.URL, func() ([]byte, bool) { return rawBody, called }
}

func TestBuild_FloatVariableSentAsJSONNumber(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Apps",
		Use:     "set-weight",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "weight", Flag: "weight", In: InVariable, GoType: "float64", Required: true, Help: "weight"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation setWeight($weight: Float!) { setWeight(weight: $weight) { id } }","variables":{}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "apps", "set-weight", "--weight", "1.5"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	vars, _ := got["variables"].(map[string]any)
	if vars["weight"] != 1.5 {
		t.Errorf("variables.weight = %#v (%T), want 1.5 (float64)", vars["weight"], vars["weight"])
	}
}

func TestBuild_IntListVariableSentAsJSONNumbers(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Apps",
		Use:     "set-ids",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "ids", Flag: "ids", In: InVariable, GoType: "[]int64", Required: true, Help: "ids"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation setIds($ids: [Int!]!) { setIds(ids: $ids) { id } }","variables":{}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "apps", "set-ids", "--ids", "1", "--ids", "2"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	vars, _ := got["variables"].(map[string]any)
	ids, ok := vars["ids"].([]any)
	if !ok || len(ids) != 2 || ids[0] != float64(1) || ids[1] != float64(2) {
		t.Errorf("variables.ids = %#v, want [1,2] as JSON numbers", vars["ids"])
	}
}

func TestBuild_RequiredQueryParamBlocksBeforeRequest(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Receivers",
		Use:     "get-receiver",
		Method:  "GET",
		PathTpl: "/receivers",
		Params: []ParamSpec{
			{Name: "type", Flag: "type", In: InQuery, GoType: "string", Required: true, Help: "Receiver type"},
		},
		Security: &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "receivers", "get-receiver"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected required flag error")
	}
	if !strings.Contains(err.Error(), "required flag") || !strings.Contains(err.Error(), "type") {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != 0 {
		t.Fatalf("server hits = %d, want 0", hits)
	}
}

func TestBuild_PaginationFlagsAttached(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Items",
		Use:     "list-items",
		Method:  "GET",
		PathTpl: "/items",
		Output: OutputHints{
			Pagination: &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token"},
		},
	}}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	items := mustFindChild(t, svc, "items")
	listItems := mustFindChild(t, items, "list-items")

	for _, name := range []string{"all", "max-pages"} {
		if listItems.Flag(name) == nil {
			t.Errorf("list-items missing --%s flag", name)
		}
	}
}

func TestBuild_WaitFlagOnMutating(t *testing.T) {
	specs := []CommandSpec{
		{Group: "Resources", Use: "create-resource", Method: "POST", PathTpl: "/resources"},
		{Group: "Resources", Use: "list-resources", Method: "GET", PathTpl: "/resources"},
	}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	resources := mustFindChild(t, svc, "resources")

	create := mustFindChild(t, resources, "create-resource")
	if create.Flag("wait") == nil {
		t.Error("create-resource (POST) should have --wait flag")
	}

	list := mustFindChild(t, resources, "list-resources")
	if list.Flag("wait") != nil {
		t.Error("list-resources (GET) should NOT have --wait flag")
	}
}

func TestBuild_ControlFlagsAvoidOperationParameterCollisions(t *testing.T) {
	names := []string{"all", "max-pages", "wait", "dry-run", "file", "set", "set-str"}
	params := make([]ParamSpec, 0, len(names))
	for _, name := range names {
		params = append(params, ParamSpec{Name: name, Flag: name, In: InQuery, GoType: "string"})
	}
	params = append(params, ParamSpec{Name: "lathe-dry-run", Flag: "lathe-dry-run", In: InQuery, GoType: "string"})
	specs := []CommandSpec{{
		Group:       "Resources",
		Use:         "create-resource",
		Method:      "POST",
		PathTpl:     "/resources",
		Params:      params,
		RequestBody: &RequestBody{MediaType: "application/json"},
		Output: OutputHints{
			Pagination: &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token"},
		},
	}}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	create := mustFindChild(t, mustFindChild(t, mustFindChild(t, root, "demo"), "resources"), "create-resource")
	for _, name := range names {
		if create.Flag(name) == nil {
			t.Errorf("create-resource missing operation parameter --%s", name)
		}
		if create.Flag("lathe-"+name) == nil {
			t.Errorf("create-resource missing collision-safe control flag --lathe-%s", name)
		}
	}
	if create.Flag("lathe-dry-run-2") == nil {
		t.Error("create-resource missing deterministic fallback --lathe-dry-run-2")
	}
}

func mustFindChild(t *testing.T, parent *cobra.Command, use string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Use == use {
			return c
		}
	}
	t.Fatalf("%s has no child %q; children = %v", parent.Use, use, cmdNames(parent.Commands()))
	return nil
}

func cmdNames(cmds []*cobra.Command) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Use)
	}
	return names
}

func isRequiredFlag(annotations map[string][]string) bool {
	for _, v := range annotations[cobra.BashCompOneRequiredFlag] {
		if v == "true" {
			return true
		}
	}
	return false
}
