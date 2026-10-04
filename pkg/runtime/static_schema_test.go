package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestStaticBodySchemaEntrypoints(t *testing.T) {
	isolateRuntime(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	spec := CommandSpec{Group: "Items", Use: "create", Method: "POST", PathTpl: "/items", Security: &SecurityHint{Public: true}, RequestBody: &RequestBody{MediaType: "application/json", Schema: &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{"count": {Type: "integer"}}, Required: []string{"count"}}}}
	file := t.TempDir() + "/body.json"
	testutil.NoError(t, os.WriteFile(file, []byte(`{"count":"private-value"}`), 0o600))
	for _, entry := range []string{"invoke", "preview", "file", "set", "set-str", "command-preview", "workflow"} {
		t.Run(entry, func(t *testing.T) {
			before := hits.Load()
			var err error
			switch entry {
			case "invoke", "preview":
				_, err = InvokeOperation(context.Background(), spec, OperationInput{HasFile: true, FileBody: []byte(`{"count":"private-value"}`)}, OperationOptions{Hostname: srv.URL, DryRun: entry == "preview"})
			case "workflow":
				root := newWorkflowRoot(io.Discard)
				testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{Use: "run", Steps: []WorkflowStepSpec{{ID: "create", Operation: spec, BodyStringSets: []WorkflowValue{{Name: "count", Value: "private-value"}}}}}}))
				root.SetArgs([]string{"--hostname", srv.URL, "run"})
				err = root.Execute()
			default:
				root := newWorkflowRoot(io.Discard)
				testutil.NoError(t, Build(root, "demo", []CommandSpec{spec}))
				args := []string{"--hostname", srv.URL, "demo", "items", "create"}
				switch entry {
				case "file":
					args = append(args, "--file", file)
				case "set":
					args = append(args, "--set", "count=private-value")
				case "set-str":
					args = append(args, "--set-str", "count=12")
				case "command-preview":
					args = append(args, "--file", file, "--dry-run")
				}
				root.SetArgs(args)
				err = root.Execute()
			}
			if err == nil {
				t.Fatal("invalid body accepted")
			}
			if hits.Load() != before {
				t.Fatal("invalid body sent to HTTP server")
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatalf("body value leaked: %v", err)
			}
			var le *LatheError
			if !errors.As(err, &le) || le.ExitCode != ExitUsage {
				t.Fatalf("expected usage error, got %T: %v", err, err)
			}
		})
	}
}

func TestStaticBodySchemaContracts(t *testing.T) {
	nested := &SchemaSpec{Type: "object", Required: []string{"items"}, Properties: map[string]*SchemaSpec{"items": {Type: "array", Items: &SchemaSpec{Type: "object", Required: []string{"count"}, Properties: map[string]*SchemaSpec{"count": {Type: "integer"}}}}}}
	for _, tc := range []struct {
		name   string
		schema *SchemaSpec
		body   string
		path   string
	}{
		{name: "nested array item", schema: nested, body: `{"items":[{"count":"private-value"}]}`, path: "#/items/0/count"},
		{name: "nested required", schema: nested, body: `{"items":[{}]}`, path: "#/items/0/count"},
		{name: "array mismatch", schema: nested, body: `{"items":{}}`, path: "#/items"},
		{name: "valid nested", schema: nested, body: `{"items":[{"count":9007199254740993}]}`},
		{name: "noninteger", schema: &SchemaSpec{Type: "integer"}, body: `9007199254740993.1`, path: "#/"},
		{name: "integer decimal", schema: &SchemaSpec{Type: "integer"}, body: `1.0`},
		{name: "integer exponent", schema: &SchemaSpec{Type: "integer"}, body: `1e0`},
		{name: "number", schema: &SchemaSpec{Type: "number"}, body: `1.5`},
		{name: "boolean", schema: &SchemaSpec{Type: "boolean"}, body: `true`},
		{name: "boolean mismatch", schema: &SchemaSpec{Type: "boolean"}, body: `"private-value"`, path: "#/"},
		{name: "null type", schema: &SchemaSpec{Type: "null"}, body: `null`},
		{name: "nonnullable", schema: &SchemaSpec{Type: "string"}, body: `null`, path: "#/"},
		{name: "nullable", schema: &SchemaSpec{Type: "string", Nullable: true}, body: `null`},
		{name: "required nullable present", schema: &SchemaSpec{Type: "object", Required: []string{"count"}, Properties: map[string]*SchemaSpec{"count": {Type: "integer", Nullable: true}}}, body: `{"count":null}`},
		{name: "typeless scalar", schema: &SchemaSpec{Properties: map[string]*SchemaSpec{"count": {Type: "integer"}}, Required: []string{"count"}}, body: `true`},
		{name: "typeless object", schema: &SchemaSpec{Properties: map[string]*SchemaSpec{"count": {Type: "integer"}}}, body: `{"count":"private-value"}`, path: "#/count"},
		{name: "reference siblings", schema: &SchemaSpec{AllOf: []*SchemaSpec{nested, {Required: []string{"name"}}}}, body: `{"items":[]}`, path: "#/name"},
		{name: "residual reference", schema: &SchemaSpec{Ref: "https://unreachable.invalid/schema"}, body: `{}`},
		{name: "unknown property allowed", schema: &SchemaSpec{Type: "object"}, body: `{"extra":true}`},
		{name: "escaped property path", schema: &SchemaSpec{Properties: map[string]*SchemaSpec{"a/b~c": {Type: "integer"}}}, body: `{"a/b~c":"private-value"}`, path: "#/a~1b~0c"},
		{name: "malformed JSON", schema: nested, body: `{"items":"private-value`, path: "valid JSON"},
		{name: "multiple JSON values", schema: &SchemaSpec{}, body: `{} {}`, path: "valid JSON"},
		{name: "without schema", body: `private-value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := CommandSpec{RequestBody: &RequestBody{MediaType: "application/problem+json; charset=utf-8", Schema: tc.schema}}
			_, _, _, err := resolveOperationRequest(spec, OperationInput{HasFile: true, FileBody: []byte(tc.body)}, ClientOptions{})
			if tc.path == "" {
				testutil.NoError(t, err)
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("expected failure at %s, got %v", tc.path, err)
			}
			for _, format := range []string{"json", "table"} {
				var out strings.Builder
				if FormatError(err, format, &out) != ExitUsage {
					t.Fatalf("expected usage exit: %s", out.String())
				}
				if !strings.Contains(out.String(), tc.path) {
					t.Fatalf("body path missing: %s", out.String())
				}
				if strings.Contains(out.String(), "private-value") {
					t.Fatalf("body leaked: %s", out.String())
				}
			}
		})
	}
	for _, mediaType := range []string{"text/plain", "application/octet-stream", "application/x-www-form-urlencoded"} {
		spec := CommandSpec{RequestBody: &RequestBody{MediaType: mediaType, Schema: nested}}
		_, _, _, err := resolveOperationRequest(spec, OperationInput{HasFile: true, FileBody: []byte("private-value")}, ClientOptions{})
		testutil.NoError(t, err)
	}
	spec := CommandSpec{RequestBody: &RequestBody{MediaType: "application/json", Schema: nested}}
	_, _, _, err := resolveOperationRequest(spec, OperationInput{}, ClientOptions{})
	testutil.NoError(t, err)
	spec.RequestBody.Template = `{"query":"query", "variables":{}}`
	spec.RequestBody.MergePath = "variables"
	spec.RequestBody.Schema = &SchemaSpec{Type: "object", Required: []string{"count"}, Properties: map[string]*SchemaSpec{"count": {Type: "integer"}}}
	_, _, _, err = resolveOperationRequest(spec, OperationInput{BodySets: []string{"count=1"}}, ClientOptions{})
	testutil.NoError(t, err)
	_, _, _, err = resolveOperationRequest(spec, OperationInput{BodyStringSets: []string{"count=private-value"}}, ClientOptions{})
	if err == nil || !strings.Contains(err.Error(), "#/variables/count") {
		t.Fatalf("template schema bypassed: %v", err)
	}
}

func TestStaticBodySchemaUnsupportedMetadata(t *testing.T) {
	spec := CommandSpec{Method: "POST", PathTpl: "/items", RequestBody: &RequestBody{Schema: &SchemaSpec{Type: "object", Required: []string{"enabled", "enabled"}, Properties: map[string]*SchemaSpec{"enabled": {Type: "boolean"}, "file": {Type: "file"}, "unknown": nil}, AllOf: []*SchemaSpec{nil}}}}
	for _, body := range []string{`{"enabled":true,"file":"data"}`, `{"enabled":false,"unknown":7}`} {
		_, err := InvokeOperation(context.Background(), spec, OperationInput{HasFile: true, FileBody: []byte(body)}, OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
		testutil.NoError(t, err)
	}
	for _, body := range []string{`{}`, `{"enabled":"yes"}`} {
		_, err := InvokeOperation(context.Background(), spec, OperationInput{HasFile: true, FileBody: []byte(body)}, OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
		testutil.Require(t, err != nil, "supported constraints were discarded")
	}
}
