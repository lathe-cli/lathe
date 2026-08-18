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

func TestValidateOperationInput_TemplatedBodySchema(t *testing.T) {
	spec := staticBodyCommandSpec()
	spec.RequestBody.Template = `{"query":"mutation($input: Input!){create(input:$input){id}}","variables":{}}`
	spec.RequestBody.MergePath = "variables"
	spec.RequestBody.Schema = &SchemaSpec{
		Type:     "object",
		Required: []string{"input"},
		Properties: map[string]*SchemaSpec{
			"input": {
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]*SchemaSpec{
					"name": {Type: "string"},
				},
			},
		},
	}

	tests := []struct {
		name  string
		input OperationInput
		want  string
	}{
		{name: "valid file", input: OperationInput{FileBody: []byte(`{"input":{"name":"demo"}}`), HasFile: true}},
		{name: "valid set", input: OperationInput{BodyStringSets: []string{"input.name=demo"}}},
		{name: "missing nested required field", input: OperationInput{FileBody: []byte(`{"input":{}}`), HasFile: true}, want: `$.input.name: required field missing`},
		{name: "wrong nested type", input: OperationInput{FileBody: []byte(`{"input":{"name":1}}`), HasFile: true}, want: `$.input.name: expected string, got number`},
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
		})
	}
}

func TestValidateOperationInput_TypelessSchemaKeywordsDoNotImplyType(t *testing.T) {
	tests := []struct {
		name   string
		schema *SchemaSpec
		body   string
		want   string
	}{
		{
			name: "object keywords permit non object",
			schema: &SchemaSpec{
				Required:   []string{"name"},
				Properties: map[string]*SchemaSpec{"name": {Type: "string"}},
			},
			body: `"value"`,
		},
		{
			name: "object keywords validate object",
			schema: &SchemaSpec{
				Required:   []string{"name"},
				Properties: map[string]*SchemaSpec{"name": {Type: "string"}},
			},
			body: `{}`,
			want: `$.name: required field missing`,
		},
		{
			name:   "items keyword permits non array",
			schema: &SchemaSpec{Items: &SchemaSpec{Type: "string"}},
			body:   `true`,
		},
		{
			name:   "items keyword validates array",
			schema: &SchemaSpec{Items: &SchemaSpec{Type: "string"}},
			body:   `[1]`,
			want:   `$[0]: expected string, got number`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = tc.schema
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_ComposedSchemas(t *testing.T) {
	tests := []struct {
		name   string
		schema *SchemaSpec
		body   string
		want   string
	}{
		{
			name: "allOf valid",
			schema: &SchemaSpec{AllOf: []*SchemaSpec{
				{Type: "object"},
				{Type: "object", Required: []string{"name"}, Properties: map[string]*SchemaSpec{"name": {Type: "string"}}},
			}},
			body: `{"name":"demo"}`,
		},
		{
			name: "allOf branch failure",
			schema: &SchemaSpec{AllOf: []*SchemaSpec{
				{Type: "object"},
				{Type: "object", Required: []string{"name"}, Properties: map[string]*SchemaSpec{"name": {Type: "string"}}},
			}},
			body: `{}`,
			want: `$.name: required field missing`,
		},
		{name: "anyOf string", schema: &SchemaSpec{AnyOf: []*SchemaSpec{{Type: "string"}, {Type: "integer"}}}, body: `"demo"`},
		{name: "anyOf integer", schema: &SchemaSpec{AnyOf: []*SchemaSpec{{Type: "string"}, {Type: "integer"}}}, body: `1`},
		{name: "anyOf failure", schema: &SchemaSpec{AnyOf: []*SchemaSpec{{Type: "string"}, {Type: "integer"}}}, body: `true`, want: `anyOf`},
		{name: "oneOf one match", schema: &SchemaSpec{OneOf: []*SchemaSpec{{Type: "number"}, {Type: "integer"}}}, body: `1.5`},
		{name: "oneOf overlapping matches", schema: &SchemaSpec{OneOf: []*SchemaSpec{{Type: "number"}, {Type: "integer"}}}, body: `1`},
		{name: "oneOf no match", schema: &SchemaSpec{OneOf: []*SchemaSpec{{Type: "string"}, {Type: "integer"}}}, body: `true`, want: `oneOf`},
		{name: "nullable composition", schema: &SchemaSpec{Nullable: true, AllOf: []*SchemaSpec{{Type: "object"}}}, body: `null`},
		{name: "non nullable composition", schema: &SchemaSpec{AllOf: []*SchemaSpec{{Type: "object"}}}, body: `null`, want: `expected object, got null`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = tc.schema
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_AdditionalProperties(t *testing.T) {
	tests := []struct {
		name   string
		schema *AdditionalPropertiesSpec
		body   string
		want   string
	}{
		{name: "schema valid", schema: &AdditionalPropertiesSpec{Allowed: true, Schema: &SchemaSpec{Type: "integer"}}, body: `{"count":1}`},
		{name: "schema invalid", schema: &AdditionalPropertiesSpec{Allowed: true, Schema: &SchemaSpec{Type: "integer"}}, body: `{"count":"bad"}`, want: `$.count: expected integer, got string`},
		{name: "allowed", schema: &AdditionalPropertiesSpec{Allowed: true}, body: `{"count":"anything"}`},
		{name: "disallowed", schema: &AdditionalPropertiesSpec{}, body: `{"count":1}`, want: `$.count: additional field not allowed`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = &SchemaSpec{Type: "object", AdditionalProperties: tc.schema}
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_RecursiveReferences(t *testing.T) {
	node := &SchemaSpec{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]*SchemaSpec{
			"name": {Type: "string"},
			"next": {Ref: "#/definitions/Node", Nullable: true},
		},
	}
	spec := staticBodyCommandSpec()
	spec.RequestBody.Schema = &SchemaSpec{Ref: "#/definitions/Node"}
	spec.RequestBody.SchemaDefinitions = map[string]*SchemaSpec{"Node": node}

	if err := validateOperationInput(spec, OperationInput{FileBody: []byte(`{"name":"one","next":{"name":"two","next":null}}`), HasFile: true}); err != nil {
		t.Fatalf("valid recursive body: %v", err)
	}
	err := validateOperationInput(spec, OperationInput{FileBody: []byte(`{"name":"one","next":{"name":1}}`), HasFile: true})
	if err == nil || !strings.Contains(err.Error(), `$.next.name: expected string, got number`) {
		t.Fatalf("error = %v, want nested recursive validation failure", err)
	}

	cycle := staticBodyCommandSpec()
	cycle.RequestBody.Schema = &SchemaSpec{Ref: "#/definitions/A"}
	cycle.RequestBody.SchemaDefinitions = map[string]*SchemaSpec{
		"A": {Ref: "#/definitions/B"},
		"B": {Ref: "#/definitions/A"},
	}
	if err := validateOperationInput(cycle, OperationInput{FileBody: []byte(`{}`), HasFile: true}); err != nil {
		t.Fatalf("alias cycle must terminate safely: %v", err)
	}
}

func TestValidateOperationInput_StringEncodedInteger(t *testing.T) {
	tests := []struct {
		name   string
		schema *SchemaSpec
		body   string
		want   string
	}{
		{name: "marked number", schema: &SchemaSpec{Type: "integer", AcceptStringEncodedInteger: true}, body: `123`},
		{name: "marked quoted integer", schema: &SchemaSpec{Type: "integer", AcceptStringEncodedInteger: true}, body: `"123"`},
		{name: "marked quoted exponent", schema: &SchemaSpec{Type: "integer", AcceptStringEncodedInteger: true}, body: `"1e3"`},
		{name: "marked quoted fraction", schema: &SchemaSpec{Type: "integer", AcceptStringEncodedInteger: true}, body: `"1.5"`, want: "expected integer, got string"},
		{name: "marked non numeric string", schema: &SchemaSpec{Type: "integer", AcceptStringEncodedInteger: true}, body: `"abc"`, want: "expected integer, got string"},
		{name: "unmarked quoted integer", schema: &SchemaSpec{Type: "integer"}, body: `"123"`, want: "expected integer, got string"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = tc.schema
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_IntegerEnum(t *testing.T) {
	tests := []struct {
		name   string
		schema *SchemaSpec
		body   string
		want   string
	}{
		{name: "marked name", schema: &SchemaSpec{Type: "string", AcceptIntegerEnum: true}, body: `"ACTIVE"`},
		{name: "marked integer", schema: &SchemaSpec{Type: "string", AcceptIntegerEnum: true}, body: `1`},
		{name: "marked fractional number", schema: &SchemaSpec{Type: "string", AcceptIntegerEnum: true}, body: `1.5`, want: "expected string, got number"},
		{name: "unmarked integer", schema: &SchemaSpec{Type: "string"}, body: `1`, want: "expected string, got number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = tc.schema
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_GraphQLID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "string", body: `"123"`},
		{name: "integer", body: `123`},
		{name: "integer exponent", body: `1e3`},
		{name: "fraction", body: `1.5`, want: "expected string, got number"},
		{name: "boolean", body: `true`, want: "expected string, got boolean"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = &SchemaSpec{Type: "string", AcceptIntegerID: true}
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_GraphQLSingletonList(t *testing.T) {
	tests := []struct {
		name   string
		schema *SchemaSpec
		body   string
		want   string
	}{
		{name: "list", schema: &SchemaSpec{Type: "array", AcceptSingletonArray: true, Items: &SchemaSpec{Type: "string"}}, body: `["one"]`},
		{name: "singleton", schema: &SchemaSpec{Type: "array", AcceptSingletonArray: true, Items: &SchemaSpec{Type: "string"}}, body: `"one"`},
		{name: "invalid singleton", schema: &SchemaSpec{Type: "array", AcceptSingletonArray: true, Items: &SchemaSpec{Type: "string"}}, body: `1`, want: "expected string, got number"},
		{name: "unmarked singleton", schema: &SchemaSpec{Type: "array", Items: &SchemaSpec{Type: "string"}}, body: `"one"`, want: "expected array, got string"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = tc.schema
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateOperationInput_StringEncodedNumber(t *testing.T) {
	tests := []struct {
		name   string
		schema *SchemaSpec
		body   string
		want   string
	}{
		{name: "marked number", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `1.5`},
		{name: "marked quoted number", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `"1.5"`},
		{name: "marked quoted exponent", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `"1e3"`},
		{name: "marked NaN", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `"NaN"`},
		{name: "marked positive infinity", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `"Infinity"`},
		{name: "marked negative infinity", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `"-Infinity"`},
		{name: "marked empty string", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `""`, want: "expected number, got string"},
		{name: "marked non numeric string", schema: &SchemaSpec{Type: "number", AcceptStringEncodedNumber: true}, body: `"abc"`, want: "expected number, got string"},
		{name: "unmarked quoted number", schema: &SchemaSpec{Type: "number"}, body: `"1.5"`, want: "expected number, got string"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticBodyCommandSpec()
			spec.RequestBody.Schema = tc.schema
			err := validateOperationInput(spec, OperationInput{FileBody: []byte(tc.body), HasFile: true})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateOperationInput: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
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
