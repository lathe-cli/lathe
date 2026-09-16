package render

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestMergeOverlayModule_RuntimeSchema(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Apps", Use: "describe-app", OperationID: "describeApp", Method: "GET", PathTpl: "/apps/{app_id}", DefaultHostname: "https://api.example.com",
			Params: []runtime.ParamSpec{
				{Name: "app_id", Flag: "app-id", In: runtime.InPath, GoType: "string", Required: true},
				{Name: "workspace_id", Flag: "workspace-id", In: runtime.InQuery, GoType: "string", Required: true, Context: "workspace"},
				{Name: "fields", Flag: "fields", In: runtime.InQuery, GoType: "string"},
			},
			Output:   runtime.OutputHints{ResponseMediaType: "application/json"},
			Security: &runtime.SecurityHint{Scopes: []string{"apps:read"}},
		},
		{
			Group: "Apps", Use: "run-app", OperationID: "runApp", Method: "POST", PathTpl: "/apps/{app_id}/run", DefaultHostname: "https://api.example.com",
			Params:      []runtime.ParamSpec{{Name: "app_id", Flag: "app-id", In: runtime.InPath, GoType: "string", Required: true}},
			RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json"},
			Security:    &runtime.SecurityHint{Scopes: []string{"apps:read", "apps:run"}},
		},
	}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"run-app": {Body: &overlay.BodyOverride{RuntimeSchema: &overlay.RuntimeSchemaOverride{
			OperationID:  "describeApp",
			ResponsePath: "input_schema",
			Params: map[string]string{
				"app_id": "${params.app-id}",
				"fields": "input_schema",
			},
		}}},
	}}
	testutil.NoError(t, ValidateOverlayModule(specs, mod))
	merged := mustMergeOverlayModule(t, specs, mod)
	binding := merged[1].RequestBody.RuntimeSchema
	testutil.Require(t, binding != nil && binding.Operation.OperationID == "describeApp" && binding.Operation.DefaultHostname == "https://api.example.com" && binding.Params["app_id"] == "${params.app-id}", "runtime schema = %#v", binding)

	chdirWithGoMod(t)
	testutil.NoError(t, RenderModule("demo", "", specs, mod.Commands))
	generated := generatedModule(t, "demo")
	for _, want := range []string{"RuntimeSchema: &runtime.RuntimeSchemaSpec{", `OperationID: "describeApp"`, `ResponsePath: "input_schema"`} {
		testutil.Check(t, containsGo(generated, want), "generated module missing %q", want)
	}

	duplicateMapping := overlay.Module{Commands: map[string]overlay.Override{
		"run-app": {Body: &overlay.BodyOverride{RuntimeSchema: &overlay.RuntimeSchemaOverride{
			OperationID:  "describeApp",
			ResponsePath: "input_schema",
			Params: map[string]string{
				"app_id": "${params.app-id}",
				"app-id": "literal",
				"fields": "input_schema",
			},
		}}},
	}}
	if err := ValidateOverlayModule(specs, duplicateMapping); err == nil || !strings.Contains(err.Error(), "mapped more than once") {
		t.Fatalf("duplicate mapping error = %v", err)
	}

	optionalTarget := []runtime.CommandSpec{cloneCommandSpec(specs[0]), cloneCommandSpec(specs[1])}
	optionalTarget[0].Params[2].Required = true
	optionalTarget[1].Params = append(optionalTarget[1].Params, runtime.ParamSpec{Name: "mode", Flag: "mode", In: runtime.InQuery, GoType: "string"})
	optionalMapping := overlay.Module{Commands: map[string]overlay.Override{
		"run-app": {Body: &overlay.BodyOverride{RuntimeSchema: &overlay.RuntimeSchemaOverride{
			OperationID:  "describeApp",
			ResponsePath: "input_schema",
			Params: map[string]string{
				"app_id": "${params.app-id}",
				"fields": "${params.mode}",
			},
		}}},
	}}
	if err := ValidateOverlayModule(optionalTarget, optionalMapping); err == nil || !strings.Contains(err.Error(), "optional target param") {
		t.Fatalf("optional target mapping error = %v", err)
	}

	for name, mutate := range map[string]func(*runtime.CommandSpec){
		"non-GET":   func(source *runtime.CommandSpec) { source.Method = "HEAD" },
		"body":      func(source *runtime.CommandSpec) { source.RequestBody = &runtime.RequestBody{} },
		"form body": func(source *runtime.CommandSpec) { source.Params[0].In = runtime.InFormData },
		"hidden":    func(source *runtime.CommandSpec) { source.Hidden = true },
		"stream":    func(source *runtime.CommandSpec) { source.Output.Streaming = &runtime.StreamingHint{Strategy: "sse"} },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := []runtime.CommandSpec{cloneCommandSpec(specs[0]), cloneCommandSpec(specs[1])}
			mutate(&invalid[0])
			if err := ValidateOverlayModule(invalid, mod); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
