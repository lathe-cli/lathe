package runtime

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
)

func TestActiveContextPrecedenceAndSuccessfulSelectorPersistence(t *testing.T) {
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/pause" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"kind\":\"paused\"}\n\n")
			return
		}
		if strings.Contains(r.URL.Path, "bad") {
			http.Error(w, "bad workspace", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	config.Bind(&config.Manifest{
		CLI:      config.CLIInfo{Name: "myctl", ConfigDir: "myctl", ConfigDirEnv: "MYCTL_CONFIG_DIR", HostEnv: "MYCTL_HOST"},
		Contexts: map[string]config.ContextInfo{"workspace": {Env: "MYCTL_WORKSPACE_ID"}},
	})
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("MYCTL_WORKSPACE_ID", "")
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	hosts.Set(srv.URL, config.HostEntry{AuthType: "bearer", OAuthToken: "token", Contexts: map[string]string{"workspace": "stored"}})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}

	list := CommandSpec{Group: "Apps", Use: "list", Method: "GET", PathTpl: "/apps", Security: &SecurityHint{Public: true}, Shortcuts: []CommandShortcut{{Use: "list-selected", Params: map[string]string{"workspace_id": "preset"}}}, Params: []ParamSpec{
		{Name: "workspace_id", Flag: "workspace-id", In: InQuery, GoType: "string", Required: true, Default: "spec-default", Context: "workspace"},
	}}
	switchWorkspace := CommandSpec{Group: "Apps", Use: "use", Method: "POST", PathTpl: "/workspaces/{workspace_id}", Security: &SecurityHint{Public: true}, Params: []ParamSpec{
		{Name: "workspace_id", Flag: "workspace-id", In: InPath, GoType: "string", Required: true},
	}, SetContext: &ContextSetHint{Name: "workspace", Param: "workspace_id"}}
	run := func(args ...string) error {
		root := newRootWithModuleGroup()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(io.Discard)
		root.PersistentFlags().String("hostname", srv.URL, "")
		root.PersistentFlags().StringP("output", "o", "raw", "")
		mustBuild(t, root, "demo", []CommandSpec{list, switchWorkspace})
		root.SetArgs(args)
		return root.Execute()
	}

	if err := run("demo", "apps", "list"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYCTL_WORKSPACE_ID", "environment")
	if err := run("demo", "apps", "list"); err != nil {
		t.Fatal(err)
	}
	if err := run("demo", "apps", "list", "--workspace-id", "explicit"); err != nil {
		t.Fatal(err)
	}
	if err := run("list-selected"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(requested[:4], "|") != "/apps?workspace_id=stored|/apps?workspace_id=environment|/apps?workspace_id=explicit|/apps?workspace_id=preset" {
		t.Fatalf("requests = %#v", requested)
	}
	if err := run("demo", "apps", "use", "--workspace-id", "selected"); err != nil {
		t.Fatal(err)
	}
	if err := run("demo", "apps", "use", "--workspace-id", "bad"); err == nil {
		t.Fatal("failed selector succeeded")
	}
	reloaded, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := reloaded.Get(srv.URL)
	if entry.Contexts["workspace"] != "selected" || entry.OAuthToken != "token" {
		t.Fatalf("entry = %+v", entry)
	}
	pause := CommandSpec{
		Method: "POST", PathTpl: "/pause",
		Params:     []ParamSpec{{Name: "workspace_id", Flag: "workspace-id", In: InQuery, GoType: "string"}},
		SetContext: &ContextSetHint{Name: "workspace", Param: "workspace_id"},
		Output: OutputHints{Streaming: &StreamingHint{Strategy: "sse", Policy: &StreamPolicy{
			DataFormat: "json", EventNamePath: "kind", Collect: &StreamCollectHint{RequireStop: true, PauseEvents: []string{"paused"}},
		}}},
	}
	result, err := InvokeOperation(t.Context(), pause, OperationInput{Values: map[string]any{"workspace_id": "paused"}}, OperationOptions{Hostname: srv.URL, Client: ClientOptions{MaxRetries: -1}})
	if err != nil || result.Outcome != OperationOutcomePaused {
		t.Fatalf("pause result = %+v, err = %v", result, err)
	}
	reloaded, _ = config.LoadHosts()
	entry, _ = reloaded.Get(srv.URL)
	if entry.Contexts["workspace"] != "selected" {
		t.Fatalf("paused operation persisted context: %+v", entry.Contexts)
	}
	t.Setenv("MYCTL_WORKSPACE_ID", "")
	if err := config.MutateHosts(t.Context(), func(hosts *config.Hosts) error {
		entry, _ := hosts.Get(srv.URL)
		delete(entry.Contexts, "workspace")
		hosts.Set(srv.URL, entry)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := run("demo", "apps", "list"); err == nil || ClassifyError(err).Code != CodeUsage {
		t.Fatalf("missing context error = %v", err)
	}
}

func TestWorkflowStepUsesStoredContext(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	config.Bind(&config.Manifest{
		CLI:      config.CLIInfo{Name: "myctl", ConfigDir: "myctl", ConfigDirEnv: "MYCTL_CONFIG_DIR", HostEnv: "MYCTL_HOST"},
		Contexts: map[string]config.ContextInfo{"workspace": {}},
	})
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())
	hosts, _ := config.LoadHosts()
	hosts.Set(srv.URL, config.HostEntry{AuthType: "bearer", OAuthToken: "token", Contexts: map[string]string{"workspace": "ws-1"}})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}
	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{{Use: "check", Steps: []WorkflowStepSpec{{ID: "workspace", Operation: CommandSpec{
		Use: "get", Method: "GET", PathTpl: "/workspaces/{workspace_id}", Security: &SecurityHint{Public: true}, Params: []ParamSpec{
			{Name: "workspace_id", Flag: "workspace-id", In: InPath, GoType: "string", Required: true, Context: "workspace"},
		},
	}}}}}); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "check"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if path != "/workspaces/ws-1" {
		t.Fatalf("path = %q", path)
	}
}
