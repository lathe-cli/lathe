package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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

type observedWriter struct {
	buf   bytes.Buffer
	once  sync.Once
	wrote chan struct{}
}

func (w *observedWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	w.once.Do(func() { close(w.wrote) })
	return n, err
}

func (w *observedWriter) String() string { return w.buf.String() }

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

func TestBuild_RejectsParamFlagCollision(t *testing.T) {
	t.Run("across parameters", func(t *testing.T) {
		root := newRootWithModuleGroup()
		err := Build(root, "demo", []CommandSpec{{
			Group: "Users",
			Use:   "update",
			Params: []ParamSpec{
				{Name: "tokenEnv", Flag: "token-env", In: InQuery, GoType: "string"},
				{Name: "token", Flag: "token", In: InBody, GoType: "string"},
			},
		}})
		if err == nil {
			t.Fatal("expected parameter flag collision error")
		}
	})
	t.Run("within sensitive aliases", func(t *testing.T) {
		root := newRootWithModuleGroup()
		err := Build(root, "demo", []CommandSpec{{
			Group: "Users",
			Use:   "update",
			Params: []ParamSpec{{
				Name: "token", Flag: "token", Aliases: []string{"token-env"}, In: InBody, GoType: "string",
			}},
		}})
		if err == nil {
			t.Fatal("expected sensitive alias binding collision error")
		}
	})
}

func TestBuild_RejectsCompletionModuleName(t *testing.T) {
	root := newRootWithModuleGroup()
	if err := Build(root, "completion", nil); err == nil || !strings.Contains(err.Error(), `module command "completion" conflicts`) {
		t.Fatalf("expected completion conflict error, got %v", err)
	}
	if len(root.Commands()) != 0 {
		t.Fatalf("completion module must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}

	root = newRootWithModuleGroup()
	err := Build(root, "demo", []CommandSpec{{
		Group:     "Users",
		Use:       "get-user",
		Method:    "GET",
		PathTpl:   "/users/{id}",
		Shortcuts: []CommandShortcut{{Use: "completion"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected completion shortcut conflict error, got %v", err)
	}
}

func TestBuildFlat_RejectsCompletionGroupName(t *testing.T) {
	root := newRootWithModuleGroup()
	err := BuildFlat(root, "demo", []CommandSpec{{Group: "Completion", Use: "list", Method: "GET", PathTpl: "/x"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected completion group conflict error, got %v", err)
	}
	if len(root.Commands()) != 0 {
		t.Fatalf("completion group must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}
}

func TestBuild_PopulatesGroupAndOpTree(t *testing.T) {
	specs := []CommandSpec{
		{
			Group:      "Users",
			GroupShort: "Manage user accounts",
			Use:        "get-user",
			Short:      "Get a user",
			Method:     "GET",
			PathTpl:    "/users/{id}",
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
	if usersGroup.Short != "Manage user accounts" {
		t.Errorf("users group short = %q", usersGroup.Short)
	}
	if itemsGroup.Short != "Items operations" {
		t.Errorf("items group fallback short = %q", itemsGroup.Short)
	}

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

func TestBuild_UnknownNestedCommandIsUsage(t *testing.T) {
	for _, args := range [][]string{{"demo", "unknown"}, {"demo", "users", "unknown"}, {"demo", "users", "get-user", "unknown"}} {
		root := newRootWithModuleGroup()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		mustBuild(t, root, "demo", []CommandSpec{{Group: "Users", Use: "get-user"}})
		root.SetArgs(args)

		err := root.Execute()
		if err == nil || ClassifyError(err).Code != CodeUsage {
			t.Fatalf("Execute(%v) error = %v, want usage", args, err)
		}
	}
}

func TestBuild_ParameterFlagAliasesAndPositionalArgument(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	tests := []struct {
		name    string
		input   []string
		wantURL string
		wantErr string
	}{
		{name: "primary flag", input: []string{"--app-id", "a-1", "--workspace-id", "w-1"}, wantURL: "/apps/a-1?workspace_id=w-1"},
		{name: "legacy flag", input: []string{"--app_id", "a-1", "--workspace_id", "w-1"}, wantURL: "/apps/a-1?workspace_id=w-1"},
		{name: "positional", input: []string{"a-1", "--workspace-id", "w-1"}, wantURL: "/apps/a-1?workspace_id=w-1"},
		{name: "positional slice", input: []string{"a-1", "one,two", "--workspace-id", "w-1"}, wantURL: "/apps/a-1?tags=one&tags=two&workspace_id=w-1"},
		{name: "conflicting inputs", input: []string{"a-1", "--app-id", "a-2", "--workspace-id", "w-1"}, wantErr: "both argument 1 and --app-id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requestURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestURL = r.URL.String()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			root := newRootWithModuleGroup()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "raw", "")
			mustBuild(t, root, "demo", []CommandSpec{{
				Group: "Apps", Use: "describe", Method: "GET", PathTpl: "/apps/{app_id}",
				Params: []ParamSpec{{
					Name: "app_id", Flag: "app-id", Aliases: []string{"app_id"}, In: InPath,
					GoType: "string", Required: true, Argument: "id",
				}, {
					Name: "workspace_id", Flag: "workspace-id", Aliases: []string{"workspace_id"}, In: InQuery,
					GoType: "string", Required: true,
				}, {
					Name: "tags", Flag: "tags", In: InQuery, GoType: "[]string", Argument: "tags",
				}},
				Security: &SecurityHint{Public: true},
			}})
			describe := findChildCommand(mustFindChild(t, mustFindChild(t, root, "demo"), "apps"), "describe")
			if describe == nil {
				t.Fatal("describe command was not mounted")
				return
			}
			if describe.Use != "describe [id] [tags]" {
				t.Fatalf("use = %q, want describe [id] [tags]", describe.Use)
			}
			args := []string{"--hostname", srv.URL, "demo", "apps", "describe"}
			root.SetArgs(append(args, tc.input...))

			err := root.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Execute error = %v, want %q", err, tc.wantErr)
				}
				if requestURL != "" {
					t.Fatalf("request sent to %q", requestURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if requestURL != tc.wantURL {
				t.Fatalf("URL = %q, want %q", requestURL, tc.wantURL)
			}
		})
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

func TestBuild_RawStreamingWritesBeforeResponseCloses(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	firstSent := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(firstSent)
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: second\n\n")
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	out := &observedWriter{wrote: make(chan struct{})}
	root := newRootWithModuleGroup()
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:    "Events",
		Use:      "watch",
		Method:   http.MethodGet,
		PathTpl:  "/events",
		Output:   OutputHints{Streaming: &StreamingHint{Strategy: "sse"}},
		Security: &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "events", "watch", "-o", "raw"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()
	select {
	case <-firstSent:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("server did not send first event")
	}
	select {
	case <-out.wrote:
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-errCh
		t.Fatal("command produced no output before the streaming response closed")
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "data: first\n\ndata: second\n\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestBuild_StreamOutputFlagCombinations(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	response := "{\"kind\":\"done\"}\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, response)
	}))
	defer srv.Close()

	spec := CommandSpec{
		Group:   "Events",
		Use:     "watch",
		Method:  http.MethodPost,
		PathTpl: "/events",
		Output: OutputHints{Streaming: &StreamingHint{Strategy: "ndjson", Policy: &StreamPolicy{
			DataFormat:    "json",
			EventNamePath: "kind",
			Collect:       &StreamCollectHint{RequireStop: true, StopEvents: []string{"done"}},
			Live:          &StreamLiveHint{Events: []string{"chunk"}, From: "text"},
		}}},
		Security: &SecurityHint{Public: true},
	}
	cases := []struct {
		name       string
		args       []string
		wantErr    string
		wantOutput string
	}{
		{name: "live with json", args: []string{"-o", "json", "demo", "events", "watch", "--stream"}, wantErr: "live stream output does not support -o json"},
		{name: "live with wait", args: []string{"demo", "events", "watch", "--stream", "--wait"}, wantErr: "live stream output does not support wait polling"},
		{name: "raw with wait", args: []string{"-o", "raw", "demo", "events", "watch", "--wait"}, wantOutput: response},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			root := newRootWithModuleGroup()
			root.SetOut(&out)
			root.SetErr(io.Discard)
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "table", "")
			mustBuild(t, root, "demo", []CommandSpec{spec})
			root.SetArgs(append([]string{"--hostname", srv.URL}, tc.args...))

			err := root.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected %q error, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if out.String() != tc.wantOutput {
				t.Fatalf("output = %q, want %q", out.String(), tc.wantOutput)
			}
		})
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
		t.Errorf("body = %#v, want %#v", got, want)
	}
}

func TestBuild_TypedBodyFlagsSendJSON(t *testing.T) {
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
		Group:   "Keys",
		Use:     "replace-limits",
		Method:  "PATCH",
		PathTpl: "/keys/{id}/limits",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", Argument: "key-id", In: InPath, GoType: "string", Required: true},
			{Name: "maxBudgetUsd", Flag: "max-budget-usd", In: InBody, GoType: "float64"},
			{Name: "budgetDuration", Flag: "budget-duration", In: InBody, GoType: "string", Enum: []string{"daily", "weekly", "monthly"}},
			{Name: "rpmLimit", Flag: "rpm-limit", In: InBody, GoType: "int64"},
			{Name: "allowedModels", Flag: "allowed-models", In: InBody, GoType: "[]string"},
			{Name: "expiresAt", Flag: "expires-at", In: InBody, GoType: "string"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
		Security:    &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{
		"--hostname", srv.URL,
		"demo", "keys", "replace-limits", "key-1",
		"--max-budget-usd", "100",
		"--budget-duration", "monthly",
		"--rpm-limit", "60",
		"--allowed-models", "model-a,model-b",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	want := map[string]any{
		"maxBudgetUsd":   float64(100),
		"budgetDuration": "monthly",
		"rpmLimit":       float64(60),
		"allowedModels":  []any{"model-a", "model-b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestBuild_RequiredBodyFieldsAcceptEveryBodyInput(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())
	bodyFile := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(bodyFile, []byte(`{"name":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		bodyRequired bool
		args         []string
		wantBody     string
	}{
		{name: "typed flag", bodyRequired: true, args: []string{"--name", "from-flag"}, wantBody: `{"name":"from-flag"}`},
		{name: "set", bodyRequired: true, args: []string{"--set", "name=from-set"}, wantBody: `{"name":"from-set"}`},
		{name: "file", bodyRequired: true, args: []string{"--file", bodyFile}, wantBody: `{"name":"from-file"}`},
		{name: "explicit null", bodyRequired: true, args: []string{"--set", "name=null"}, wantBody: `{"name":null}`},
		{name: "optional body omitted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			root := newRootWithModuleGroup()
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "raw", "")
			mustBuild(t, root, "demo", []CommandSpec{{
				Group:   "Users",
				Use:     "create-user",
				Method:  "POST",
				PathTpl: "/users",
				Params: []ParamSpec{{
					Name: "name", Flag: "name", In: InBody, GoType: "string", Required: true,
				}},
				RequestBody: &RequestBody{Required: tc.bodyRequired, MediaType: "application/json"},
				Security:    &SecurityHint{Public: true},
			}})
			args := []string{"--hostname", srv.URL, "demo", "users", "create-user"}
			root.SetArgs(append(args, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if gotBody != tc.wantBody {
				t.Fatalf("body = %q, want %q", gotBody, tc.wantBody)
			}
		})
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

func TestBuild_MultipartSendsFileAndFields(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	type capture struct {
		contentType string
		disposition string
		filename    string
		fileType    string
		fileBody    string
		queryValue  string
		bodyValue   string
		err         error
	}
	var got capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.contentType = r.Header.Get("Content-Type")
		got.queryValue = r.URL.Query().Get("purpose")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			got.err = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		got.bodyValue = strings.Join(r.MultipartForm.Value["purpose"], ",")
		file, header, err := r.FormFile("file")
		if err != nil {
			got.err = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			got.err = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		got.disposition = header.Header.Get("Content-Disposition")
		got.filename = header.Filename
		got.fileType = header.Header.Get("Content-Type")
		got.fileBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"file-1"}`))
	}))
	defer srv.Close()

	filePath := t.TempDir() + "/sample.png"
	if err := os.WriteFile(filePath, []byte("file-content"), 0o600); err != nil {
		t.Fatalf("write upload: %v", err)
	}

	root := newRootWithModuleGroup()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Uploads",
		Use:     "create",
		Method:  http.MethodPost,
		PathTpl: "/uploads",
		Params: []ParamSpec{
			{Name: "purpose", Flag: "purpose", In: InQuery, GoType: "string"},
			{Name: "file", Flag: "file", In: InFormData, GoType: "string", Required: true, Format: "binary"},
			{Name: "purpose", Flag: "body-purpose", In: InFormData, GoType: "string"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "multipart/form-data"},
		Security:    &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "uploads", "create", "--file", filePath, "--purpose", "query", "--body-purpose", "body"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.err != nil {
		t.Fatalf("parse multipart: %v", got.err)
	}
	if !strings.HasPrefix(got.contentType, "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	if got.filename != "sample.png" || got.fileType != "image/png" || got.fileBody != "file-content" {
		t.Errorf("file = filename %q, type %q, body %q", got.filename, got.fileType, got.fileBody)
	}
	disposition, dispositionParams, err := mime.ParseMediaType(got.disposition)
	if err != nil || disposition != "form-data" || dispositionParams["name"] != "file" || dispositionParams["filename"] != "sample.png" {
		t.Errorf("Content-Disposition = %q: %v", got.disposition, err)
	}
	if got.queryValue != "query" || got.bodyValue != "body" {
		t.Errorf("purpose = query %q, body %q", got.queryValue, got.bodyValue)
	}
}

func TestBuild_MultipartFileErrorPrecedesAuth(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	root := newRootWithModuleGroup()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", []CommandSpec{{
		Group: "Uploads", Use: "create", Method: http.MethodPost, PathTpl: "/uploads",
		Params:      []ParamSpec{{Name: "file", Flag: "file", In: InFormData, GoType: "string", Required: true, Format: "binary"}},
		RequestBody: &RequestBody{Required: true, MediaType: "multipart/form-data"},
	}})
	root.SetArgs([]string{"--hostname", "https://example.invalid", "demo", "uploads", "create", "--file", t.TempDir() + "/missing"})

	err := root.Execute()
	if err == nil || ClassifyError(err).Code != CodeUsage || !strings.Contains(err.Error(), "read multipart file") {
		t.Fatalf("error = %v, want local multipart usage error before auth", err)
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
		}})
		root.SetArgs(append([]string{"--hostname", "http://127.0.0.1:1", "demo", "exports", "create-export"}, args...))

		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "requires --file") {
			t.Fatalf("Execute error = %v, want requires --file", err)
		}
		if got := ClassifyError(err).Code; got != CodeUsage {
			t.Fatalf("error code = %q, want %q", got, CodeUsage)
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
	if strings.Contains(stdout.String(), "dry-run-query-secret") || strings.Contains(stdout.String(), "dry-run-key-secret") || strings.Contains(stdout.String(), "dry-run-body-secret") || strings.Contains(stdout.String(), "dry-run-array-secret") || strings.Contains(stdout.String(), "another-array-secret") {
		t.Fatalf("dry-run leaked credential: %s", stdout.String())
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
	if out.Body["value"] != "***" {
		t.Fatalf("sensitive body field = %#v", out.Body["value"])
	}
	if out.Body["values"] != "***" {
		t.Fatalf("sensitive body array = %#v", out.Body["values"])
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
	if !out.Auth.Required || out.Auth.Public {
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

	root.SetArgs([]string{"--hostname", url, "demo", "credentials", "create-credential", "--input_api_key-env", "OPENAI_API_KEY"})
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
			{Name: "input.apiKey", Flag: "input-api-key", Aliases: []string{"input_api_key"}, In: InVariable, GoType: "string", Required: true, Help: "API key"},
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

func TestBuild_InvalidOutputBlocksBeforeRequest(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	root := newRootWithModuleGroup()
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	mustBuild(t, root, "demo", []CommandSpec{{
		Group: "Items", Use: "get-item", Method: "GET", PathTpl: "/items/1", Security: &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "--output", "not-a-format", "demo", "items", "get-item"})

	err := root.Execute()
	if err == nil || ClassifyError(err).Code != CodeUsage {
		t.Fatalf("error = %v, want usage", err)
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

func TestBuild_EnumUsageErrorSafeDetail(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	specs := []CommandSpec{{
		Group:   "Usage",
		Use:     "summary",
		Method:  "GET",
		PathTpl: "/usage",
		Params: []ParamSpec{
			{Name: "range", Flag: "range", In: InQuery, GoType: "string", Enum: []string{"7", "30"}},
		},
		Security: &SecurityHint{Public: true},
	}}
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"demo", "usage", "summary", "--range", "14"})

	err := root.Execute()
	var le *LatheError
	if !errors.As(err, &le) {
		t.Fatalf("expected LatheError, got %v", err)
	}
	if le.Code != CodeUsage {
		t.Fatalf("code = %q, want %q", le.Code, CodeUsage)
	}
	if le.Detail != "--range accepts: 7, 30" {
		t.Fatalf("detail = %q", le.Detail)
	}
	if strings.Contains(le.Detail, "14") {
		t.Fatalf("detail echoed user input: %q", le.Detail)
	}
}

func TestBuild_RequiredFlagUsageErrorDetail(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "get",
		Method:  "GET",
		PathTpl: "/keys/{id}",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true},
		},
		Security: &SecurityHint{Public: true},
	}}
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"demo", "keys", "get"})

	err := root.Execute()
	var le *LatheError
	if !errors.As(err, &le) {
		t.Fatalf("expected LatheError, got %v", err)
	}
	if le.Detail != "missing required: --id" {
		t.Fatalf("detail = %q", le.Detail)
	}
}

func TestBuild_MissingBodyFieldUsageErrorDetail(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "create",
		Method:  "POST",
		PathTpl: "/keys",
		Params: []ParamSpec{
			{Name: "name", Flag: "name", In: InVariable, GoType: "string", Required: true},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
		Security:    &SecurityHint{Public: true},
	}}
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"demo", "keys", "create", "--set", "other=1"})

	err := root.Execute()
	var le *LatheError
	if !errors.As(err, &le) {
		t.Fatalf("expected LatheError, got %v", err)
	}
	if le.Detail != "missing required: name" {
		t.Fatalf("detail = %q", le.Detail)
	}
}

func TestBuild_RequiredBodyUsageErrorDetail(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	tests := []struct {
		name       string
		params     []ParamSpec
		wantDetail string
	}{
		{
			name:       "with body flags",
			params:     []ParamSpec{{Name: "name", Flag: "name", In: InBody, GoType: "string"}},
			wantDetail: "request body required: pass --file, --set, --set-str, or a body flag",
		},
		{
			name:       "without body flags",
			wantDetail: "request body required: pass --file, --set, or --set-str",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			specs := []CommandSpec{{
				Group:       "Keys",
				Use:         "create",
				Method:      "POST",
				PathTpl:     "/keys",
				Params:      tc.params,
				RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
				Security:    &SecurityHint{Public: true},
			}}
			root := newRootWithModuleGroup()
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "table", "")
			root.SilenceErrors = true
			root.SilenceUsage = true
			mustBuild(t, root, "demo", specs)
			root.SetArgs([]string{"demo", "keys", "create"})

			err := root.Execute()
			var le *LatheError
			if !errors.As(err, &le) {
				t.Fatalf("expected LatheError, got %v", err)
			}
			if le.Code != CodeUsage {
				t.Fatalf("code = %q, want %q", le.Code, CodeUsage)
			}
			if le.Detail != tc.wantDetail {
				t.Fatalf("detail = %q, want %q", le.Detail, tc.wantDetail)
			}
		})
	}
}

func TestBuild_UnsupportedOutputFormatDetail(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	specs := []CommandSpec{{
		Group:    "Usage",
		Use:      "summary",
		Method:   "GET",
		PathTpl:  "/usage",
		Security: &SecurityHint{Public: true},
	}}
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"-o", "bogus-format", "demo", "usage", "summary"})

	err := root.Execute()
	var le *LatheError
	if !errors.As(err, &le) {
		t.Fatalf("expected LatheError, got %v", err)
	}
	want := "--output accepts: " + strings.Join(FormatterNames(), ", ")
	if le.Detail != want {
		t.Fatalf("detail = %q, want %q", le.Detail, want)
	}
	if strings.Contains(le.Detail, "bogus-format") {
		t.Fatalf("detail echoed user input: %q", le.Detail)
	}
}

func TestBuild_APIErrorKnownErrorFallbackDetail(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	cases := []struct {
		name        string
		contentType string
		body        string
		known       []KnownError
		wantDetail  string
	}{
		{
			name:        "declared message wins over known error",
			contentType: "application/json",
			body:        `{"message":"key was revoked upstream"}`,
			known:       []KnownError{{Status: 403, Cause: "the API key is revoked"}},
			wantDetail:  "key was revoked upstream",
		},
		{
			name:        "known error fallback for non-json body",
			contentType: "text/plain",
			body:        "upstream-secret",
			known:       []KnownError{{Status: 403, Cause: "the API key is revoked"}},
			wantDetail:  "the API key is revoked",
		},
		{
			name:        "status mismatch keeps detail empty",
			contentType: "text/plain",
			body:        "upstream-secret",
			known:       []KnownError{{Status: 404, Cause: "no such key"}},
			wantDetail:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			specs := []CommandSpec{{
				Group:       "Keys",
				Use:         "reveal",
				Method:      "GET",
				PathTpl:     "/keys/reveal",
				KnownErrors: tc.known,
				Security:    &SecurityHint{Public: true},
			}}
			root := newRootWithModuleGroup()
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "table", "")
			root.SilenceErrors = true
			root.SilenceUsage = true
			mustBuild(t, root, "demo", specs)
			root.SetArgs([]string{"--hostname", srv.URL, "demo", "keys", "reveal"})

			err := root.Execute()
			var le *LatheError
			if !errors.As(err, &le) {
				t.Fatalf("expected LatheError, got %v", err)
			}
			if le.Code != CodeAPIError || le.Message != "API request failed" {
				t.Fatalf("error = %#v", le)
			}
			if le.Detail != tc.wantDetail {
				t.Fatalf("detail = %q, want %q", le.Detail, tc.wantDetail)
			}
			if strings.Contains(le.Detail, "upstream-secret") {
				t.Fatalf("detail leaked raw body: %q", le.Detail)
			}
		})
	}
}

func TestBuild_SetOnlyBodyFieldsHelpAndCoexistence(t *testing.T) {
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
		Group:   "Keys",
		Use:     "create",
		Method:  "POST",
		PathTpl: "/keys",
		Params: []ParamSpec{
			{Name: "name", Flag: "name", In: InBody, GoType: "string", Required: true, Help: "name (body, required)"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json", SetOnlyFields: []string{"limits"}},
		Security:    &SecurityHint{Public: true},
	}}
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	mustBuild(t, root, "demo", specs)

	cmd, _, err := root.Find([]string{"demo", "keys", "create"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"set", "set-str"} {
		usage := cmd.Flags().Lookup(flag).Usage
		if !strings.Contains(usage, "limits") {
			t.Fatalf("--%s help missing set-only field note: %q", flag, usage)
		}
	}

	root.SetArgs([]string{
		"--hostname", srv.URL,
		"demo", "keys", "create",
		"--name", "demo",
		"--set", "limits.maxBudgetUsd=3",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	want := map[string]any{
		"name":   "demo",
		"limits": map[string]any{"maxBudgetUsd": float64(3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v, want %#v", got, want)
	}
}

func TestBuild_RequiredSetOnlyBodyFieldValidatedLocally(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "create",
		Method:  "POST",
		PathTpl: "/keys",
		Params: []ParamSpec{
			{Name: "name", Flag: "name", In: InBody, GoType: "string", Required: true, Help: "name (body, required)"},
		},
		RequestBody: &RequestBody{
			Required:      true,
			MediaType:     "application/json",
			Schema:        &SchemaSpec{Type: "object", Required: []string{"name", "limits"}, Properties: map[string]*SchemaSpec{"name": {Type: "string"}, "limits": {Type: "object"}}},
			SetOnlyFields: []string{"limits"},
		},
		Security: &SecurityHint{Public: true},
	}}
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)

	root.SetArgs([]string{"--hostname", srv.URL, "demo", "keys", "create", "--name", "demo"})
	err := root.Execute()
	var le *LatheError
	if !errors.As(err, &le) {
		t.Fatalf("expected LatheError for missing required set-only field, got %v", err)
	}
	if le.Code != CodeUsage || le.Detail != "missing required: limits" {
		t.Fatalf("error = %#v", le)
	}

	root.SetArgs([]string{"--hostname", srv.URL, "demo", "keys", "create", "--name", "demo", "--set", "limits.rpm=3"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute with --set limits: %v", err)
	}
}
