package normalize

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestExpandJSONBodyFlags(t *testing.T) {
	spec := runtime.CommandSpec{
		Use:    "replace-limits",
		Method: "PATCH",
		Params: []runtime.ParamSpec{{Name: "id", Flag: "id", Argument: "key-id", In: runtime.InPath, GoType: "string", Required: true}},
		RequestBody: &runtime.RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &runtime.SchemaSpec{
				Type: "object",
				Properties: map[string]*runtime.SchemaSpec{
					"maxBudgetUsd":        {Type: "number", Nullable: true},
					"budgetDuration":      {Type: "string", Nullable: true, Enum: []string{"daily", "weekly", "monthly"}},
					"rpmLimit":            {Type: "integer", Nullable: true},
					"allowedModels":       {Type: "array", Nullable: true, Items: &runtime.SchemaSpec{Type: "string", Enum: []string{"model-a", "model-b"}}},
					"expiresAt":           {Type: "string", Format: "date-time", Nullable: true},
					"maxParallelRequests": {Type: "integer", Nullable: true},
				},
			},
		},
	}
	got, setOnly, err := ExpandJSONBodyFlags(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(setOnly) != 0 {
		t.Fatalf("setOnly = %#v, want empty", setOnly)
	}
	byName := map[string]runtime.ParamSpec{}
	for _, param := range got {
		byName[param.Name] = param
		if param.In != runtime.InBody {
			t.Errorf("%s In = %q", param.Name, param.In)
		}
		if param.Required {
			t.Errorf("%s unexpectedly required", param.Name)
		}
	}
	if byName["maxBudgetUsd"].Flag != "max-budget-usd" || byName["maxBudgetUsd"].GoType != "float64" {
		t.Fatalf("maxBudgetUsd = %+v", byName["maxBudgetUsd"])
	}
	if byName["budgetDuration"].Flag != "budget-duration" || !equalStrings(byName["budgetDuration"].Enum, []string{"daily", "weekly", "monthly"}) {
		t.Fatalf("budgetDuration = %+v", byName["budgetDuration"])
	}
	if byName["rpmLimit"].GoType != "int64" {
		t.Fatalf("rpmLimit = %+v", byName["rpmLimit"])
	}
	if byName["allowedModels"].GoType != "[]string" || !equalStrings(byName["allowedModels"].ItemEnum, []string{"model-a", "model-b"}) {
		t.Fatalf("allowedModels = %+v", byName["allowedModels"])
	}
	if byName["expiresAt"].Format != "date-time" {
		t.Fatalf("expiresAt = %+v", byName["expiresAt"])
	}
}

func TestExpandJSONBodyFlags_RequiredAndCollision(t *testing.T) {
	spec := runtime.CommandSpec{
		Use:    "create",
		Params: []runtime.ParamSpec{{Name: "file", Flag: "file", In: runtime.InQuery, GoType: "string"}},
		RequestBody: &runtime.RequestBody{
			MediaType: "application/json",
			Schema: &runtime.SchemaSpec{
				Type:       "object",
				Required:   []string{"name"},
				Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}, "file": {Type: "string"}},
			},
		},
	}
	if _, _, err := ExpandJSONBodyFlags(spec); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("error = %v, want flag conflict", err)
	}
	spec.Params = nil
	spec.RequestBody.Schema.Properties = map[string]*runtime.SchemaSpec{"name": {Type: "string"}, "enabled": {Type: "boolean"}}
	got, _, err := ExpandJSONBodyFlags(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Name != "name" || !got[1].Required || !strings.Contains(got[1].Help, "required") {
		t.Fatalf("params = %#v", got)
	}
}

func TestExpandJSONBodyFlags_RejectsUnsupported(t *testing.T) {
	cases := []struct {
		name string
		spec runtime.CommandSpec
		want string
	}{
		{
			name: "only nested properties",
			spec: jsonBodySpec(&runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"limits": {Type: "object", Properties: map[string]*runtime.SchemaSpec{"rpm": {Type: "integer"}}}}}),
			want: "no body properties support typed flags",
		},
		{
			name: "oneof",
			spec: jsonBodySpec(&runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"value": {OneOf: []*runtime.SchemaSpec{{Type: "string"}, {Type: "integer"}}}}}),
			want: "oneOf/anyOf/allOf",
		},
		{
			name: "map",
			spec: jsonBodySpec(&runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"labels": {Type: "object", AdditionalProperties: &runtime.AdditionalPropertiesSpec{Allowed: true}}}}),
			want: "maps",
		},
		{
			name: "graphql",
			spec: runtime.CommandSpec{RequestBody: &runtime.RequestBody{MediaType: "application/json", Template: `{"query":"q"}`, Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}}}}},
			want: "GraphQL",
		},
		{
			name: "multipart",
			spec: runtime.CommandSpec{RequestBody: &runtime.RequestBody{MediaType: "multipart/form-data", Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}}}}},
			want: "multipart",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ExpandJSONBodyFlags(tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExpandJSONBodyFlags_SkipsNestedObjectProperties(t *testing.T) {
	spec := jsonBodySpec(&runtime.SchemaSpec{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]*runtime.SchemaSpec{
			"name":   {Type: "string"},
			"limits": {Type: "object", Properties: map[string]*runtime.SchemaSpec{"maxBudgetUsd": {Type: "number"}}},
			"admins": {Type: "array", Items: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"id": {Type: "string"}}}},
		},
	})
	got, setOnly, err := ExpandJSONBodyFlags(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "name" || got[0].Flag != "name" || !got[0].Required {
		t.Fatalf("params = %#v", got)
	}
	if !equalStrings(setOnly, []string{"admins", "limits"}) {
		t.Fatalf("setOnly = %#v", setOnly)
	}
}

func jsonBodySpec(schema *runtime.SchemaSpec) runtime.CommandSpec {
	return runtime.CommandSpec{RequestBody: &runtime.RequestBody{MediaType: "application/json", Schema: schema}}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
