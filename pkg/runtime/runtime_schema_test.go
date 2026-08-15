package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
)

func TestInvokeOperation_RuntimeSchema(t *testing.T) {
	var schemaHits atomic.Int32
	var targetHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apps/app-1":
			schemaHits.Add(1)
			if r.URL.Query().Get("workspace_id") != "ws-1" {
				t.Errorf("workspace_id = %q", r.URL.Query().Get("workspace_id"))
			}
			if r.URL.Query().Get("fields") != "input_schema" {
				t.Errorf("fields = %q", r.URL.Query().Get("fields"))
			}
			if got := r.URL.Query()["mode"]; len(got) != 2 || got[0] != "chat" || got[1] != "workflow" {
				t.Errorf("mode = %#v", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"input_schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"count":{"type":"integer"},"note":{"type":["string","null"]}},"required":["count"],"additionalProperties":false}}`)
		case "/apps/app-1/run":
			targetHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	config.Bind(&config.Manifest{
		CLI:      config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Contexts: map[string]config.ContextInfo{"workspace": {}},
	})
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	hosts.Set(srv.URL, config.HostEntry{Contexts: map[string]string{"workspace": "ws-1"}})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}

	appID := ParamSpec{Name: "app_id", Flag: "app-id", In: InPath, GoType: "string", Required: true}
	source := CommandSpec{
		OperationID: "describeApp",
		Method:      "GET",
		PathTpl:     "/apps/{app_id}",
		Params: []ParamSpec{
			appID,
			{Name: "workspace_id", Flag: "workspace-id", In: InQuery, GoType: "string", Context: "workspace"},
			{Name: "selected_workspace", Flag: "selected-workspace", In: InQuery, GoType: "string"},
			{Name: "fields", Flag: "fields", In: InQuery, GoType: "string"},
			{Name: "mode", Flag: "mode", In: InQuery, GoType: "[]string"},
		},
		SetContext: &ContextSetHint{Name: "workspace", Param: "selected_workspace"},
		Output:     OutputHints{ResponseMediaType: "application/json"},
	}
	target := CommandSpec{
		OperationID: "runApp",
		Method:      "POST",
		PathTpl:     "/apps/{app_id}/run",
		Params: []ParamSpec{
			appID,
			{Name: "mode", Flag: "mode", In: InQuery, GoType: "[]string"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			RuntimeSchema: &RuntimeSchemaSpec{
				Operation:    source,
				ResponsePath: "input_schema",
				Params: map[string]string{
					"app_id":             "${params.app_id}",
					"fields":             "input_schema",
					"mode":               "${params.mode}",
					"selected_workspace": "ws-mutated",
				},
			},
		},
	}
	baseInput := OperationInput{
		Values:  map[string]any{"app_id": "app-1", "mode": []string{"chat", "workflow"}},
		Changed: map[string]bool{"app_id": true, "mode": true},
	}
	opts := OperationOptions{Hostname: srv.URL, Client: ClientOptions{MaxRetries: -1}}

	invalid := baseInput
	invalid.FileBody = []byte(`{"count":2.0,"extra":true}`)
	invalid.HasFile = true
	if _, err := InvokeOperation(t.Context(), target, invalid, opts); err == nil || ClassifyError(err).Code != CodeUsage {
		t.Fatalf("invalid body error = %v, want usage", err)
	}
	if schemaHits.Load() != 1 || targetHits.Load() != 0 {
		t.Fatalf("invalid body hits = schema:%d target:%d", schemaHits.Load(), targetHits.Load())
	}
	hosts, err = config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := hosts.Get(srv.URL)
	if entry.Contexts["workspace"] != "ws-1" {
		t.Fatalf("schema preflight persisted context = %#v", entry.Contexts)
	}

	valid := baseInput
	valid.FileBody = []byte(`{"count":2.0,"note":null}`)
	valid.HasFile = true
	if _, err := InvokeOperation(t.Context(), target, valid, opts); err != nil {
		t.Fatalf("valid body: %v", err)
	}
	if schemaHits.Load() != 2 || targetHits.Load() != 1 {
		t.Fatalf("valid body hits = schema:%d target:%d", schemaHits.Load(), targetHits.Load())
	}

	optional := target
	optional.RequestBody = &RequestBody{MediaType: "application/json", RuntimeSchema: target.RequestBody.RuntimeSchema}
	if _, err := InvokeOperation(t.Context(), optional, baseInput, opts); err != nil {
		t.Fatalf("optional body: %v", err)
	}
	if schemaHits.Load() != 2 || targetHits.Load() != 2 {
		t.Fatalf("optional body hits = schema:%d target:%d", schemaHits.Load(), targetHits.Load())
	}

	dryRun := opts
	dryRun.DryRun = true
	if result, err := InvokeOperation(t.Context(), target, valid, dryRun); err != nil || result.DryRun == nil {
		t.Fatalf("dry-run result = %+v, err = %v", result, err)
	}
	if schemaHits.Load() != 2 || targetHits.Load() != 2 {
		t.Fatalf("dry-run hits = schema:%d target:%d", schemaHits.Load(), targetHits.Load())
	}
}

func TestInvokeOperation_RuntimeSchemaDoesNotLoadExternalRefs(t *testing.T) {
	var externalHits atomic.Int32
	var targetHits atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/schema":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"schema":{"$ref":%q}}`, srv.URL+"/external")
		case "/external":
			externalHits.Add(1)
			fmt.Fprint(w, `{}`)
		case "/run":
			targetHits.Add(1)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()

	target := CommandSpec{
		Method:  "POST",
		PathTpl: "/run",
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			RuntimeSchema: &RuntimeSchemaSpec{
				Operation:    CommandSpec{Method: "GET", PathTpl: "/schema"},
				ResponsePath: "schema",
			},
		},
	}
	input := OperationInput{FileBody: []byte(`{}`), HasFile: true}
	_, err := InvokeOperation(t.Context(), target, input, OperationOptions{Hostname: srv.URL, Client: ClientOptions{MaxRetries: -1}})
	if classified := ClassifyError(err); classified == nil || classified.Code != CodeAPIError {
		t.Fatalf("error = %v, want api_error", err)
	}
	if externalHits.Load() != 0 || targetHits.Load() != 0 {
		t.Fatalf("hits = external:%d target:%d", externalHits.Load(), targetHits.Load())
	}
}
