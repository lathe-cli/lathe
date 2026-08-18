package runtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestValidateOperationInput_StaticBodySchema(t *testing.T) {
	spec := staticBodyCommandSpec()
	tests := []struct {
		name  string
		input OperationInput
		want  string
	}{
		{
			name:  "valid file",
			input: OperationInput{FileBody: []byte(`{"name":"demo","profile":{"age":2.0},"tags":["one"],"active":true,"score":1.5,"note":null}`), HasFile: true},
		},
		{
			name: "valid sets",
			input: OperationInput{
				BodySets:       []string{"profile.age=2", "tags[0]=one", "active=true", "score=1.5", "note=null"},
				BodyStringSets: []string{"name=demo"},
			},
		},
		{
			name:  "invalid json",
			input: OperationInput{FileBody: []byte(`{`), HasFile: true},
			want:  "decode request body JSON",
		},
		{
			name:  "missing nested required field",
			input: OperationInput{FileBody: []byte(`{"name":"demo","profile":{},"tags":["one"],"active":true,"score":1.5}`), HasFile: true},
			want:  "$.profile.age: required field missing",
		},
		{
			name:  "wrong scalar type",
			input: OperationInput{FileBody: []byte(`{"name":"demo","profile":{"age":"top-secret"},"tags":["one"],"active":true,"score":1.5}`), HasFile: true},
			want:  "$.profile.age: expected integer, got string",
		},
		{
			name:  "wrong array item type",
			input: OperationInput{FileBody: []byte(`{"name":"demo","profile":{"age":2},"tags":["one",3],"active":true,"score":1.5}`), HasFile: true},
			want:  "$.tags[1]: expected string, got number",
		},
		{
			name:  "wrong boolean type",
			input: OperationInput{FileBody: []byte(`{"name":"demo","profile":{"age":2},"tags":["one"],"active":"true","score":1.5}`), HasFile: true},
			want:  "$.active: expected boolean, got string",
		},
		{
			name:  "wrong number type",
			input: OperationInput{FileBody: []byte(`{"name":"demo","profile":{"age":2},"tags":["one"],"active":true,"score":"1.5"}`), HasFile: true},
			want:  "$.score: expected number, got string",
		},
		{
			name:  "non nullable null",
			input: OperationInput{FileBody: []byte(`{"name":null,"profile":{"age":2},"tags":["one"],"active":true,"score":1.5}`), HasFile: true},
			want:  "$.name: expected string, got null",
		},
		{
			name:  "wrong root type",
			input: OperationInput{FileBody: []byte(`[]`), HasFile: true},
			want:  "$: expected object, got array",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOperationInput(spec, tc.input)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "top-secret") {
				t.Fatalf("error exposed body value: %v", err)
			}
		})
	}

	withoutSchema := spec
	withoutSchema.RequestBody = &RequestBody{Required: true, MediaType: "application/json"}
	if err := validateOperationInput(withoutSchema, OperationInput{FileBody: []byte(`{`), HasFile: true}); err != nil {
		t.Fatalf("command without schema changed behavior: %v", err)
	}
}

func TestStaticBodySchemaRejectsExecutionBeforeTransport(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	assertRejected := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "$.profile.age") {
			t.Errorf("error = %v, want profile.age validation failure", err)
		}
		if err != nil && strings.Contains(err.Error(), "top-secret") {
			t.Errorf("error exposed body value: %v", err)
		}
		if got := hits.Swap(0); got != 0 {
			t.Errorf("transport hits = %d, want 0", got)
		}
	}

	for _, dryRun := range []bool{false, true} {
		name := "command"
		if dryRun {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			root := newRootWithModuleGroup()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "raw", "")
			mustBuild(t, root, "demo", []CommandSpec{staticBodyCommandSpec()})
			args := []string{
				"--hostname", srv.URL,
				"demo", "users", "create",
				"--set-str", "name=demo",
				"--set-str", "profile.age=top-secret",
				"--set", "tags[0]=one",
				"--set", "active=true",
				"--set", "score=1.5",
			}
			if dryRun {
				args = append(args, "--dry-run")
			}
			root.SetArgs(args)
			assertRejected(t, root.Execute())
		})
	}

	t.Run("workflow", func(t *testing.T) {
		root := newWorkflowRoot(io.Discard)
		if err := BuildWorkflows(root, []WorkflowSpec{{
			Use: "create-invalid",
			Steps: []WorkflowStepSpec{{
				ID:        "create",
				Operation: staticBodyCommandSpec(),
				BodySets: []WorkflowValue{
					{Name: "tags[0]", Value: "one"},
					{Name: "active", Value: "true"},
					{Name: "score", Value: "1.5"},
				},
				BodyStringSets: []WorkflowValue{
					{Name: "name", Value: "demo"},
					{Name: "profile.age", Value: "top-secret"},
				},
			}},
		}}); err != nil {
			t.Fatalf("BuildWorkflows: %v", err)
		}
		root.SetArgs([]string{"--hostname", srv.URL, "create-invalid"})
		assertRejected(t, root.Execute())
	})
}

func staticBodyCommandSpec() CommandSpec {
	return CommandSpec{
		Group:   "Users",
		Use:     "create",
		Method:  http.MethodPost,
		PathTpl: "/users",
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &SchemaSpec{
				Type:     "object",
				Required: []string{"name", "profile", "tags", "active", "score"},
				Properties: map[string]*SchemaSpec{
					"name":    {Type: "string"},
					"profile": {Type: "object", Required: []string{"age"}, Properties: map[string]*SchemaSpec{"age": {Type: "integer"}}},
					"tags":    {Type: "array", Items: &SchemaSpec{Type: "string"}},
					"active":  {Type: "boolean"},
					"score":   {Type: "number"},
					"note":    {Type: "string", Nullable: true},
				},
			},
		},
		Security: &SecurityHint{Public: true},
	}
}
