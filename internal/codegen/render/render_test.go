package render

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func chdirWithGoMod(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func chdirWithGeneratedRoot(t *testing.T) {
	t.Helper()
	chdirWithGoMod(t)
	if err := os.MkdirAll("internal/generated", 0o755); err != nil {
		t.Fatal(err)
	}
}

func generatedModule(t *testing.T, name string) string {
	t.Helper()
	out, err := os.ReadFile(filepath.Join("internal/generated", name, name+"_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func generatedModules(t *testing.T) string {
	t.Helper()
	out, err := os.ReadFile("internal/generated/modules_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func generatedWorkflows(t *testing.T) string {
	t.Helper()
	out, err := os.ReadFile("internal/generated/workflows/workflows_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRenderModule_AppliesOverlay(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/api/v1/addon", Params: []runtime.ParamSpec{{Name: "workspace_id", Flag: "workspace-id", In: runtime.InQuery, GoType: "string"}}, RequestBody: &runtime.RequestBody{Required: true, Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}}}}, Output: runtime.OutputHints{DefaultColumns: []string{"id"}}},
		{Group: "Addon", Use: "untouched", Short: "untouched short", Method: "GET", PathTpl: "/api/v1/x"},
	}
	overrides := map[string]overlay.Override{
		"install-addon": {
			Aliases: []string{"addon-install"},
			Short:   "OVERLAY SHORT",
			Long:    "OVERLAY LONG DESC",
			Example: "myctl demo install-addon --name foo",
			Examples: []overlay.Example{{
				Summary: "Install from JSON",
				Command: "myctl demo install-addon --file addon.json -o json",
				BodyShape: map[string]any{
					"input": map[string]any{"name": "foo"},
				},
				OutputHints:      overlay.ExampleOutputHints{IDPath: "data.installAddon.id"},
				FollowUpCommands: []string{"myctl demo get-addon --id <id> -o json"},
			}},
			Notes:         []string{"Use the canonical addon ID."},
			Prerequisites: []string{"List clusters before installing."},
			KnownErrors:   []overlay.KnownError{{Status: 400, Cause: "missing addon name"}},
			Params:        map[string]overlay.ParamOverride{"workspace_id": {Context: "workspace"}},
			Context:       &overlay.ContextOverride{SetOnSuccess: &overlay.ContextSetOnSuccess{Name: "workspace", FromParam: "workspace_id"}},
			Output:        &overlay.OutputOverride{DefaultColumns: []string{"name"}, ColumnLabels: map[string]string{"name": "Addon"}},
		},
	}

	if err := RenderModule("demo", "", specs, overrides); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	got := generatedModule(t, "demo")

	for _, want := range []string{
		`"OVERLAY SHORT"`,
		`"OVERLAY LONG DESC"`,
		`"myctl demo install-addon --name foo"`,
		`Examples: []runtime.CommandExample{`,
		`Summary: "Install from JSON"`,
		`Command: "myctl demo install-addon --file addon.json -o json"`,
		`BodyShape: []byte("{\"input\":{\"name\":\"foo\"}}")`,
		`OutputHints: &runtime.ExampleOutputHints{IDPath: "data.installAddon.id"}`,
		`FollowUpCommands: []string{"myctl demo get-addon --id <id> -o json"}`,
		`"addon-install"`,
		`Notes:`,
		`"Use the canonical addon ID."`,
		`Prerequisites:`,
		`"List clusters before installing."`,
		`KnownErrors:`,
		`[]runtime.KnownError{`,
		`Status: 400`,
		`Cause: "missing addon name"`,
		`Context: "workspace"`,
		`SetContext: &runtime.ContextSetHint{Name: "workspace", Param: "workspace_id"}`,
		`DefaultColumns: []string{"name"}`,
		`ColumnLabels: map[string]string{"name": "Addon"}`,
		`"untouched short"`,
		`generatedSchemaVersion`,
		`func Mount(root *cobra.Command) error`,
		`if err := runtime.AssertSchema(generatedSchemaVersion); err != nil`,
		`return err`,
		`return runtime.Build(root, "demo", Specs)`,
		`Schema:`,
		`&runtime.SchemaSpec{`,
		`Properties: map[string]*runtime.SchemaSpec`,
		`"name":`,
		`Type: "string"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(got, `"raw short"`) {
		t.Errorf("overlay did not replace Short; raw value leaked into output")
	}
}

func TestRenderWorkflows_EmitsPointerFieldLiterals(t *testing.T) {
	chdirWithGeneratedRoot(t)

	specs := []runtime.WorkflowSpec{{
		Use: "doctor",
		Steps: []runtime.WorkflowStepSpec{{
			ID: "create",
			Operation: runtime.CommandSpec{
				Group:       "Apps",
				Use:         "create-app",
				Short:       "Create an app.",
				Method:      "POST",
				PathTpl:     "/apps",
				OperationID: "Apps_Create",
				RequestBody: &runtime.RequestBody{
					Required:  true,
					MediaType: "application/json",
					Schema: &runtime.SchemaSpec{
						Type:     "object",
						Required: []string{"name"},
					},
				},
				Output: runtime.OutputHints{
					Pagination: &runtime.PaginationHint{
						Strategy:   "token",
						TokenParam: "page_token",
					},
				},
				Security: &runtime.SecurityHint{Scopes: []string{"apps:write"}},
			},
		}},
	}}

	if err := RenderWorkflows(specs); err != nil {
		t.Fatalf("RenderWorkflows: %v", err)
	}
	got := generatedWorkflows(t)
	for _, want := range []string{
		`RequestBody: &runtime.RequestBody{`,
		`Schema: &runtime.SchemaSpec{`,
		`Pagination: &runtime.PaginationHint{`,
		`Security: &runtime.SecurityHint{`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
	for _, bad := range []string{"(*runtime.RequestBody)", "(*runtime.SecurityHint)", "(*runtime.PaginationHint)", "(0x"} {
		if strings.Contains(got, bad) {
			t.Fatalf("workflow literal contains pointer address %q:\n%s", bad, got)
		}
	}
}

func TestRenderModule_EmitsRequestBodyEnvelope(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{{
		Group: "Apps", Use: "create-app", Short: "Create an app.", Method: "POST", PathTpl: "/graphql",
		RequestBody: &runtime.RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &runtime.SchemaSpec{
				Type: "object",
				Properties: map[string]*runtime.SchemaSpec{
					"input": {Type: "object", Required: []string{"name"}},
				},
				Required: []string{"input"},
			},
			Template:  `{"query":"mutation CreateApp($name:String!){createApp(name:$name){id}}","variables":{}}`,
			MergePath: "variables",
		},
	}}

	if err := RenderModule("demo", "", specs, nil); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	got := generatedModule(t, "demo")

	for _, want := range []string{
		`Template:`,
		`createApp(name:$name)`,
		`MergePath: "variables"`,
		`Required: []string{`,
		`"input"`,
		`"name"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRenderModule_IgnoreDropsCommand(t *testing.T) {
	chdirWithGoMod(t)
	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "install", Method: "POST", PathTpl: "/addon"},
		{Group: "Addon", Use: "delete-addon", Short: "delete", Method: "DELETE", PathTpl: "/addon/{id}"},
	}
	overrides := map[string]overlay.Override{
		"delete-addon": {Ignore: true},
	}
	if err := RenderModule("demo", "", specs, overrides); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	got := generatedModule(t, "demo")
	if !strings.Contains(got, `"install-addon"`) {
		t.Error("install-addon should be present")
	}
	if strings.Contains(got, `"delete-addon"`) {
		t.Error("delete-addon should be ignored")
	}
}

func TestRenderModule_GroupAndHiddenOverride(t *testing.T) {
	chdirWithGoMod(t)
	hidden := true
	specs := []runtime.CommandSpec{
		{Group: "Default", Use: "get-item", Short: "get", Method: "GET", PathTpl: "/item"},
	}
	mod := overlay.Module{
		Groups: map[string]overlay.GroupOverride{
			"Items": {Short: "Inspect inventory items"},
		},
		Commands: map[string]overlay.Override{
			"get-item": {Group: "Items", Hidden: &hidden},
		},
	}
	if err := ValidateOverlayModule(specs, mod); err != nil {
		t.Fatalf("ValidateOverlayModule: %v", err)
	}
	merged := MergeOverlayModule(specs, mod)
	if merged[0].GroupShort != "Inspect inventory items" {
		t.Fatalf("group short = %q", merged[0].GroupShort)
	}
	if err := renderModuleSpecs("demo", "demo", merged); err != nil {
		t.Fatalf("renderModuleSpecs: %v", err)
	}
	got := generatedModule(t, "demo")
	if strings.Contains(got, `"Default"`) {
		t.Error("group should be overridden; Default should not appear")
	}
	if !strings.Contains(got, `"Items"`) {
		t.Error("group should be overridden to Items")
	}
	if !strings.Contains(got, `GroupShort: "Inspect inventory items"`) {
		t.Error("group short should be generated")
	}
	if !strings.Contains(got, "Hidden:") {
		t.Error("hidden should be set")
	}
}

func TestValidateOverlayModule_RejectsInvalidGroups(t *testing.T) {
	specs := []runtime.CommandSpec{{Group: "Users", Use: "list-users"}}
	for _, tc := range []struct {
		name  string
		group string
		short string
		want  string
	}{
		{name: "unknown", group: "Missing", short: "Manage missing resources", want: `group "Missing" does not exist`},
		{name: "empty short", group: "Users", want: `group "Users" short must be one non-empty trimmed line`},
		{name: "multiline short", group: "Users", short: "Manage\nusers", want: `group "Users" short must be one non-empty trimmed line`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOverlayModule(specs, overlay.Module{Groups: map[string]overlay.GroupOverride{
				tc.group: {Short: tc.short},
			}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestMergeOverlayModule_IgnoresCollisionBeforeDisambiguation(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/v1/groups"},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/v2/groups"},
	}})
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"list": {Match: overlay.OperationMatch{Path: "/v2/groups"}, Ignore: true},
	}}
	merged := MergeOverlayModule(specs, mod)
	if len(merged) != 1 {
		t.Fatalf("merged command count = %d, want 1", len(merged))
	}
	if got := merged[0].Use; got != "list" {
		t.Fatalf("surviving Use = %q, want list", got)
	}
	if got := merged[0].PathTpl; got != "/v1/groups" {
		t.Fatalf("surviving path = %q, want /v1/groups", got)
	}
}

func TestMergeOverlayModule_PreservesLegacyCollisionOverlayKey(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/v1/groups"},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/v2/groups"},
	}})

	t.Run("ignore", func(t *testing.T) {
		mod := overlay.Module{Commands: map[string]overlay.Override{
			"list-2": {Match: overlay.OperationMatch{Path: "/v2/groups"}, Ignore: true},
		}}
		if err := ValidateOverlayModule(specs, mod); err != nil {
			t.Fatalf("ValidateOverlayModule: %v", err)
		}
		merged := MergeOverlayModule(specs, mod)
		if len(merged) != 1 || merged[0].PathTpl != "/v1/groups" || merged[0].Use != "list" {
			t.Fatalf("merged = %#v, want only /v1/groups as list", merged)
		}
	})

	t.Run("override", func(t *testing.T) {
		mod := overlay.Module{Commands: map[string]overlay.Override{
			"list-2": {Match: overlay.OperationMatch{Path: "/v2/groups"}, Short: "Legacy second command"},
		}}
		if err := ValidateOverlayModule(specs, mod); err != nil {
			t.Fatalf("ValidateOverlayModule: %v", err)
		}
		merged := MergeOverlayModule(specs, mod)
		if len(merged) != 2 || merged[0].Short == "Legacy second command" || merged[1].Short != "Legacy second command" {
			t.Fatalf("merged = %#v, want override only on second command", merged)
		}
	})

	t.Run("unsuffixed override remains first", func(t *testing.T) {
		merged := MergeOverlayModule(specs, overlay.Module{Commands: map[string]overlay.Override{
			"list": {Short: "First command only"},
		}})
		if len(merged) != 2 || merged[0].Short != "First command only" || merged[1].Short == "First command only" {
			t.Fatalf("merged = %#v, want unsuffixed override only on first command", merged)
		}
	})
}

func TestMergeOverlayModule_DisambiguatesSurvivingCollisions(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/Groups"},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/groups"},
	}})
	merged := MergeOverlayModule(specs, overlay.Module{})
	if len(merged) != 2 {
		t.Fatalf("merged command count = %d, want 2", len(merged))
	}
	if merged[0].Use == merged[1].Use {
		t.Fatalf("colliding commands both use %q", merged[0].Use)
	}
}

func TestRenderModule_ParamOverride(t *testing.T) {
	chdirWithGoMod(t)
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "list-users", Short: "list", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "status", Flag: "status", In: "query", GoType: "string", Help: "original help"},
				{Name: "legacy", Flag: "legacy", In: "query", GoType: "string", Help: "legacy help"},
			},
		},
	}
	overrides := map[string]overlay.Override{
		"list-users": {
			Params: map[string]overlay.ParamOverride{
				"status": {Flag: "user-status", Argument: "state", Help: "override help", Default: "active", Deprecated: true},
				"legacy": {DeprecatedAlias: true},
			},
		},
	}
	merged := MergeOverlay(specs, overrides)
	if merged[0].Params[0].Argument != "state" {
		t.Fatalf("merged positional mapping = %#v", merged[0].Params[0])
	}
	if err := RenderModule("demo", "", specs, overrides); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	got := generatedModule(t, "demo")
	if !strings.Contains(got, `"user-status"`) {
		t.Error("flag should be renamed to user-status")
	}
	if !strings.Contains(got, `"override help"`) {
		t.Error("help should be overridden")
	}
	if !strings.Contains(got, `Default: "active"`) {
		t.Error("default should be set to active")
	}
	if strings.Contains(got, `"original help"`) {
		t.Error("original help should not appear")
	}
	if strings.Count(got, `Deprecated: true`) != 2 {
		t.Errorf("deprecated and legacy hidden alias should both mark params deprecated; output:\n%s", got)
	}
	if !strings.Contains(got, `Argument: "state"`) {
		t.Errorf("positional mapping should be generated; output:\n%s", got)
	}
}

func TestValidateOverlayModule_RejectsUnknownArgumentParameter(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "Users", Use: "get-user", Params: []runtime.ParamSpec{{Name: "id", Flag: "id"}},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"get-user": {Params: map[string]overlay.ParamOverride{"missing": {Argument: "id"}}},
	}}

	err := ValidateOverlayModule(specs, mod)
	if err == nil || !strings.Contains(err.Error(), `argument parameter "missing" does not exist`) {
		t.Fatalf("validation error = %v", err)
	}
}

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
	if err := ValidateOverlayModule(specs, mod); err != nil {
		t.Fatalf("ValidateOverlayModule: %v", err)
	}
	merged := MergeOverlayModule(specs, mod)
	binding := merged[1].RequestBody.RuntimeSchema
	if binding == nil || binding.Operation.OperationID != "describeApp" || binding.Operation.DefaultHostname != "https://api.example.com" || binding.Params["app_id"] != "${params.app-id}" {
		t.Fatalf("runtime schema = %#v", binding)
	}

	chdirWithGoMod(t)
	if err := RenderModule("demo", "", specs, mod.Commands); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	generated := generatedModule(t, "demo")
	for _, want := range []string{"RuntimeSchema: &runtime.RuntimeSchemaSpec{", `OperationID: "describeApp"`, `ResponsePath: "input_schema"`} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated module missing %q", want)
		}
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

func TestMergeOverlay_ParamRequiredOverride(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "get-user", Short: "get", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "type", Flag: "type", In: "query", GoType: "string", Help: "original help"},
			},
		},
	}

	merged := MergeOverlay(specs, map[string]overlay.Override{
		"get-user": {
			Params: map[string]overlay.ParamOverride{
				"type":    {Required: true, Help: "override help"},
				"missing": {Required: true},
			},
		},
	})

	if len(merged) != 1 {
		t.Fatalf("merged specs = %d, want 1", len(merged))
	}
	if len(merged[0].Params) != 1 {
		t.Fatalf("params = %d, want 1", len(merged[0].Params))
	}
	param := merged[0].Params[0]
	if !param.Required {
		t.Fatalf("required = false, want true")
	}
	if param.Help != "override help" {
		t.Fatalf("help = %q, want override help", param.Help)
	}
}

func TestMergeOverlay_UseRename(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group:   "Repos",
		Use:     "create-repo",
		Aliases: []string{"new-repo"},
	}}
	merged := MergeOverlay(specs, map[string]overlay.Override{
		"create-repo": {Use: "create", Aliases: []string{"new"}},
	})

	if got := merged[0].Use; got != "create" {
		t.Fatalf("Use = %q, want create", got)
	}
	if !reflect.DeepEqual(merged[0].Aliases, []string{"new-repo", "new"}) {
		t.Fatalf("aliases = %#v", merged[0].Aliases)
	}
}

func TestMergeOverlay_PathScopedRename(t *testing.T) {
	specs := []runtime.CommandSpec{
		{Group: "GatewayService", Use: "create-service", OperationID: "GatewayService_CreateService", Method: "POST", PathTpl: "/apis/v1alpha1/services"},
		{Group: "GatewayService", Use: "create-service", OperationID: "GatewayService_CreateService", Method: "POST", PathTpl: "/apis/v1alpha2/services"},
	}
	_, err := ResolveFlatCommandPath("namespaced", 1, specs)
	if err == nil || !strings.Contains(err.Error(), "/apis/v1alpha1/services") || !strings.Contains(err.Error(), "/apis/v1alpha2/services") {
		t.Fatalf("conflict error = %v", err)
	}

	merged := MergeOverlay(specs, map[string]overlay.Override{
		"create-service": {Match: overlay.OperationMatch{Method: "POST", Path: "/apis/v1alpha2/services"}, Use: "create-service-v1alpha2"},
	})
	if merged[0].Use != "create-service" || merged[1].Use != "create-service-v1alpha2" {
		t.Fatalf("uses = %q, %q", merged[0].Use, merged[1].Use)
	}
	if _, err := ResolveFlatCommandPath("namespaced", 1, merged); err != nil {
		t.Fatalf("renamed command should not conflict: %v", err)
	}
}

func TestMergeOverlayModule_BulkPaginationDefaults(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "list-users", Short: "list users", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "page", Flag: "page", In: "query", GoType: "string"},
				{Name: "pageSize", Flag: "page-size", In: "query", GoType: "string"},
			},
		},
		{
			Group: "Users", Use: "query-users", Short: "query users", Method: "GET", PathTpl: "/users/query",
			Params: []runtime.ParamSpec{
				{Name: "page", Flag: "page", In: "query", GoType: "string"},
				{Name: "pageSize", Flag: "page-size", In: "query", GoType: "string"},
			},
		},
		{
			Group: "Users", Use: "get-user", Short: "get user", Method: "GET", PathTpl: "/users/{id}",
			Params: []runtime.ParamSpec{
				{Name: "id", Flag: "id", In: "path", GoType: "string"},
			},
		},
	}

	bulk := MergeOverlayModule(specs, overlay.Module{
		Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
			MatchCommands: []string{"list-*", "query-*"},
			Params:        map[string]string{"page": "1", "pageSize": "20"},
		}},
		Commands: map[string]overlay.Override{
			"query-users": {Params: map[string]overlay.ParamOverride{"page": {Default: "7"}}},
		},
	})
	explicit := MergeOverlay(specs, map[string]overlay.Override{
		"list-users": {
			Params: map[string]overlay.ParamOverride{
				"page":     {Default: "1"},
				"pageSize": {Default: "20"},
			},
		},
		"query-users": {
			Params: map[string]overlay.ParamOverride{
				"page":     {Default: "7"},
				"pageSize": {Default: "20"},
			},
		},
	})

	if !reflect.DeepEqual(bulk, explicit) {
		t.Fatalf("bulk defaults differ from explicit overrides:\nbulk: %#v\nexplicit: %#v", bulk, explicit)
	}
	if got := paramDefault(t, bulk, "get-user", "id"); got != "" {
		t.Fatalf("non-matching command default = %q, want empty", got)
	}
}

func TestMergeOverlayModule_BulkDefaultsUseLegacyCollisionName(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/v1/groups", Parameters: []rawir.RawParameter{{Name: "page", In: "query", Type: "integer"}}},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/v2/groups", Parameters: []rawir.RawParameter{{Name: "page", In: "query", Type: "integer"}}},
	}})
	merged := MergeOverlayModule(specs, overlay.Module{Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
		MatchCommands: []string{"list-2"},
		Params:        map[string]string{"page": "2"},
	}}})
	if len(merged) != 2 || merged[0].Params[0].Default != "" || merged[1].Params[0].Default != "2" {
		t.Fatalf("merged = %#v, want bulk default only on list-2", merged)
	}
}

func TestMergeOverlayModule_BulkDefaultsDoNotReplaceSpecDefaults(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "list-users", Short: "list users", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "page", Flag: "page", In: "query", GoType: "string", Default: "5"},
				{Name: "pageSize", Flag: "page-size", In: "query", GoType: "string"},
			},
		},
	}

	merged := MergeOverlayModule(specs, overlay.Module{
		Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
			MatchCommands: []string{"list-*"},
			Params:        map[string]string{"page": "1", "pageSize": "20"},
		}},
		Commands: map[string]overlay.Override{
			"list-users": {Params: map[string]overlay.ParamOverride{"page": {Default: "9"}}},
		},
	})

	if got := paramDefault(t, merged, "list-users", "page"); got != "9" {
		t.Fatalf("per-command default = %q, want 9", got)
	}
	if got := paramDefault(t, merged, "list-users", "pageSize"); got != "20" {
		t.Fatalf("bulk pageSize default = %q, want 20", got)
	}

	withoutCommandOverride := MergeOverlayModule(specs, overlay.Module{
		Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
			MatchCommands: []string{"list-*"},
			Params:        map[string]string{"page": "1"},
		}},
	})
	if got := paramDefault(t, withoutCommandOverride, "list-users", "page"); got != "5" {
		t.Fatalf("spec default = %q, want 5", got)
	}
}

func TestMergeOverlayModule_StreamPolicy(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "Runs", Use: "run", Method: "POST", PathTpl: "/runs",
		Output: runtime.OutputHints{Streaming: &runtime.StreamingHint{Strategy: "sse"}},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"run": {Output: &overlay.OutputOverride{Streaming: &overlay.StreamingOverride{
			Data: "json", EventNamePath: "kind",
			Collect: &overlay.StreamCollect{
				RequireStop: true, StopEvents: []string{"done"},
				Fields: []overlay.StreamFieldRule{{Events: []string{"chunk"}, From: "text", To: "answer", Reduce: "concat"}},
			},
			Live: &overlay.StreamLive{Events: []string{"chunk"}, From: "text"},
		}}},
	}}
	if err := ValidateOverlayModule(specs, mod); err != nil {
		t.Fatalf("ValidateOverlayModule: %v", err)
	}
	merged := MergeOverlayModule(specs, mod)
	policy := merged[0].Output.Streaming.Policy
	if policy == nil || policy.Collect == nil || policy.Collect.Fields[0].Reduce != "concat" || policy.Live == nil {
		t.Fatalf("policy = %#v", policy)
	}
	if specs[0].Output.Streaming.Policy != nil {
		t.Fatal("merge mutated source spec")
	}

	bad := specs
	bad[0].Output.Streaming = nil
	if err := ValidateOverlayModule(bad, mod); err == nil || !strings.Contains(err.Error(), "not declared as streaming") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestMergeOverlayModule_OutputColumns(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "Resources", Use: "list", Method: "GET", PathTpl: "/resources",
		Output: runtime.OutputHints{ListPath: "items", DefaultColumns: []string{"id", "category"}},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"list": {Output: &overlay.OutputOverride{
			DefaultColumns: []string{"resourceId", "displayName"},
			ColumnLabels:   map[string]string{"resourceId": "Resource ID", "displayName": "Name"},
		}},
	}}
	if err := ValidateOverlayModule(specs, mod); err != nil {
		t.Fatalf("ValidateOverlayModule: %v", err)
	}
	merged := MergeOverlayModule(specs, mod)
	if got := strings.Join(merged[0].Output.DefaultColumns, ","); got != "resourceId,displayName" {
		t.Fatalf("default columns = %q", got)
	}
	if got := merged[0].Output.ColumnLabels["resourceId"]; got != "Resource ID" {
		t.Fatalf("column label = %q", got)
	}
	if got := strings.Join(specs[0].Output.DefaultColumns, ","); got != "id,category" {
		t.Fatalf("source default columns = %q", got)
	}

	for _, columns := range [][]string{{"displayName", "displayName"}, {"status..phase"}, {" name"}} {
		bad := overlay.Module{Commands: map[string]overlay.Override{
			"list": {Output: &overlay.OutputOverride{DefaultColumns: columns}},
		}}
		if err := ValidateOverlayModule(specs, bad); err == nil {
			t.Fatalf("ValidateOverlayModule accepted columns %#v", columns)
		}
	}

	for _, labels := range []map[string]string{
		{"unknown": "Unknown"},
		{"resourceId": ""},
		{"resourceId": " Resource ID"},
		{"resourceId": "Resource\tID"},
		{"resourceId": "Resource\nID"},
	} {
		bad := overlay.Module{Commands: map[string]overlay.Override{
			"list": {Output: &overlay.OutputOverride{
				DefaultColumns: []string{"resourceId", "displayName"},
				ColumnLabels:   labels,
			}},
		}}
		if err := ValidateOverlayModule(specs, bad); err == nil {
			t.Fatalf("ValidateOverlayModule accepted labels %#v", labels)
		}
	}
}

func TestRenderModule_NilOverrides(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/x"},
	}
	if err := RenderModule("demo", "", specs, nil); err != nil {
		t.Fatalf("RenderModule nil overrides: %v", err)
	}
	if !strings.Contains(generatedModule(t, "demo"), `"raw short"`) {
		t.Errorf("expected raw short preserved when overrides is nil")
	}
}

func paramDefault(t *testing.T, specs []runtime.CommandSpec, use string, name string) string {
	t.Helper()
	for _, spec := range specs {
		if spec.Use != use {
			continue
		}
		for _, param := range spec.Params {
			if param.Name == name {
				return param.Default
			}
		}
		t.Fatalf("param %s not found on command %s", name, use)
	}
	t.Fatalf("command %s not found", use)
	return ""
}

func TestRenderModulesGen_PropagatesMountErrors(t *testing.T) {
	chdirWithGeneratedRoot(t)

	if err := RenderModulesGen([]ModuleMount{{Name: "alpha"}, {Name: "beta"}}); err != nil {
		t.Fatalf("RenderModulesGen: %v", err)
	}
	got := generatedModules(t)
	for _, want := range []string{
		`func MountModules(root *cobra.Command) error`,
		`if err := alpha.Mount(root); err != nil`,
		`if err := beta.Mount(root); err != nil`,
		`return err`,
		`return nil`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRenderModulesGen_UsesFlatMount(t *testing.T) {
	chdirWithGeneratedRoot(t)

	if err := RenderModulesGen([]ModuleMount{{Name: "alpha", Flat: true}}); err != nil {
		t.Fatalf("RenderModulesGen: %v", err)
	}
	if got := generatedModules(t); !strings.Contains(got, `if err := alpha.MountFlat(root); err != nil`) {
		t.Fatalf("output did not use MountFlat:\n%s", got)
	}
}

func TestRenderModulesGen_WithSkillBundle(t *testing.T) {
	chdirWithGeneratedRoot(t)

	if err := RenderModulesGenWithOptions([]ModuleMount{{Name: "alpha"}}, ModulesGenOptions{
		SkillBundle: &SkillBundleMount{Root: "acmectl"},
	}); err != nil {
		t.Fatalf("RenderModulesGenWithOptions: %v", err)
	}
	got := generatedModules(t)
	for _, want := range []string{
		`func Mount(root *cobra.Command) error`,
		`return MountModules(root)`,
		`lathekitup "github.com/lathe-cli/kitup/go"`,
		`lathekitupcobra "github.com/lathe-cli/kitup/go-cobra"`,
		`latheruntime.AttachCapability(root, latheruntime.CapabilitySkillBundle)`,
		`lathegeneratedskillbundle "example.com/fake/internal/generated/skillbundle"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestRenderSkillBundlePackage(t *testing.T) {
	chdirWithGeneratedRoot(t)
	if err := os.MkdirAll("skills/acmectl/agents", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("skills/acmectl/SKILL.md", []byte("---\nname: acmectl\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("skills/acmectl/.lathe-skill", []byte("owner"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("skills/acmectl/agents/openai.yaml", []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("internal/generated/skillbundle/otherctl", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("internal/generated/skillbundle/otherctl/SKILL.md", []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RenderSkillBundlePackage("skills/acmectl", "acmectl"); err != nil {
		t.Fatalf("RenderSkillBundlePackage: %v", err)
	}
	for _, path := range []string{
		"internal/generated/skillbundle/skillbundle_gen.go",
		"internal/generated/skillbundle/acmectl/SKILL.md",
		"internal/generated/skillbundle/acmectl/agents/openai.yaml",
		"internal/generated/skillbundle/otherctl/SKILL.md",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	if _, err := os.Stat("internal/generated/skillbundle/acmectl/.lathe-skill"); !os.IsNotExist(err) {
		t.Fatalf("dotfile should be skipped, stat err = %v", err)
	}
	got, err := os.ReadFile("internal/generated/skillbundle/skillbundle_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `//go:embed acmectl/**`) {
		t.Fatalf("embed bridge missing root:\n%s", got)
	}
}

func TestResolveFlatCommandPath(t *testing.T) {
	specs := []runtime.CommandSpec{{Group: "Users", Use: "list-users"}}
	flat, err := ResolveFlatCommandPath("auto", 1, specs)
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if !flat {
		t.Fatal("auto should flat mount a single non-conflicting module")
	}

	flat, err = ResolveFlatCommandPath("auto", 1, []runtime.CommandSpec{
		{Group: "Pets", Use: "list-pets"},
		{Group: "Pets", Use: "get-pet"},
	})
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if !flat {
		t.Fatal("auto should flat mount multiple operations in the same group")
	}

	flat, err = ResolveFlatCommandPath("auto", 2, specs)
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if flat {
		t.Fatal("auto should keep multiple modules namespaced")
	}

	flat, err = ResolveFlatCommandPath("auto", 1, []runtime.CommandSpec{{Group: "Search", Use: "query"}})
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if flat {
		t.Fatal("auto should keep conflicting single modules namespaced")
	}

	flat, err = ResolveFlatCommandPath("auto", 1, []runtime.CommandSpec{{Group: "Search API", Use: "query"}})
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if flat {
		t.Fatal("auto should keep Cobra-normalized root command conflicts namespaced")
	}

	flat, err = ResolveFlatCommandPath("auto", 1, []runtime.CommandSpec{{Group: "Skill", Use: "install-skill"}})
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if flat {
		t.Fatal("auto should keep skill root command conflicts namespaced")
	}

	flat, err = ResolveFlatCommandPath("auto", 1, []runtime.CommandSpec{
		{Group: "Users", Use: "list-users"},
		{Group: "Users API", Use: "get-user"},
	})
	if err != nil {
		t.Fatalf("ResolveFlatCommandPath: %v", err)
	}
	if flat {
		t.Fatal("auto should keep duplicate Cobra-normalized groups namespaced")
	}

	_, err = ResolveFlatCommandPath("flat", 1, []runtime.CommandSpec{{Group: "Search", Use: "query"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected flat conflict error, got %v", err)
	}

	_, err = ResolveFlatCommandPath("flat", 1, []runtime.CommandSpec{{Group: "Search API", Use: "query"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected Cobra-normalized flat conflict error, got %v", err)
	}

	_, err = ResolveFlatCommandPath("flat", 1, []runtime.CommandSpec{{Group: "Skill", Use: "install-skill"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected skill flat conflict error, got %v", err)
	}

	_, err = ResolveFlatCommandPath("flat", 1, []runtime.CommandSpec{
		{Group: "Pets", Use: "list-pets"},
		{Group: "Pets", Use: "get-pet"},
	})
	if err != nil {
		t.Fatalf("same group operations should not conflict: %v", err)
	}

	_, err = ResolveFlatCommandPath("flat", 1, []runtime.CommandSpec{
		{Group: "Users", Use: "list-users"},
		{Group: "Users API", Use: "get-user"},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected duplicate group flat conflict error, got %v", err)
	}

	renamed := MergeOverlay([]runtime.CommandSpec{
		{Group: "Repos", Use: "create-repo", OperationID: "Repos_CreateRepo"},
		{Group: "Repos", Use: "create", OperationID: "Repos_Create"},
	}, map[string]overlay.Override{
		"create-repo": {Use: "create"},
	})
	_, err = ResolveFlatCommandPath("namespaced", 1, renamed)
	if err == nil || !strings.Contains(err.Error(), `command path "repos create" conflicts`) {
		t.Fatalf("expected renamed command conflict error, got %v", err)
	}

	aliased := MergeOverlay([]runtime.CommandSpec{
		{Group: "Users", Use: "get-user", OperationID: "Users_GetUser", Method: "GET", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "remove-user", OperationID: "Users_RemoveUser", Method: "DELETE", PathTpl: "/users/{id}"},
	}, map[string]overlay.Override{
		"remove-user": {Aliases: []string{"get-user"}},
	})
	_, err = ResolveFlatCommandPath("namespaced", 1, aliased)
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("expected canonical command alias conflict error, got %v", err)
	}
}

func TestRewriteCommandExamples_NormalizesMultiWordGroupPaths(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group:   "Payment API",
		Use:     "list-payments",
		Example: "acmectl billing payment api list-payments -o json",
		Examples: []runtime.CommandExample{{
			Command:          "acmectl billing payment api list-payments --file payment.json -o json",
			FollowUpCommands: []string{"acmectl billing payment api get-payment --id <id> -o json"},
		}},
	}}

	got := RewriteCommandExamples("acmectl", "billing", specs, true)
	if got[0].Example != "acmectl payment list-payments -o json" {
		t.Fatalf("flat example = %q", got[0].Example)
	}
	if got[0].Examples[0].Command != "acmectl payment list-payments --file payment.json -o json" {
		t.Fatalf("flat structured example = %q", got[0].Examples[0].Command)
	}
	if got[0].Examples[0].FollowUpCommands[0] != "acmectl billing payment api get-payment --id <id> -o json" {
		t.Fatalf("flat follow-up = %q", got[0].Examples[0].FollowUpCommands[0])
	}

	got = RewriteCommandExamples("acmectl", "billing", specs, false)
	if got[0].Example != "acmectl billing payment list-payments -o json" {
		t.Fatalf("namespaced example = %q", got[0].Example)
	}
	if got[0].Examples[0].Command != "acmectl billing payment list-payments --file payment.json -o json" {
		t.Fatalf("namespaced structured example = %q", got[0].Examples[0].Command)
	}
}

func TestValidateModuleNames(t *testing.T) {
	if err := ValidateModuleNames([]string{"pets", "billing"}); err != nil {
		t.Fatalf("distinct module names should pass: %v", err)
	}
	for _, reserved := range []string{"__lathe", "auth", "commands", "completion", "help", "login", "search", "skill", "update"} {
		err := ValidateModuleNames([]string{"pets", reserved})
		if err == nil || !strings.Contains(err.Error(), "reserved root command") {
			t.Fatalf("module name %q should be rejected, got %v", reserved, err)
		}
	}
	err := ValidateModuleNames([]string{"pets", "Skill API"})
	if err == nil || !strings.Contains(err.Error(), "reserved root command") {
		t.Fatalf("Cobra-normalized module name should be rejected, got %v", err)
	}
	err = ValidateModuleNames([]string{"pets", "pets"})
	if err == nil || !strings.Contains(err.Error(), "mounted more than once") {
		t.Fatalf("duplicate module names should be rejected, got %v", err)
	}
	err = ValidateModuleNames([]string{"pets", "Pets API"})
	if err == nil || !strings.Contains(err.Error(), "mounted more than once") {
		t.Fatalf("Cobra-normalized duplicate module names should be rejected, got %v", err)
	}
}

func TestRenderWorkflows_EmitsStepConditions(t *testing.T) {
	chdirWithGeneratedRoot(t)

	specs := []runtime.WorkflowSpec{{
		Use: "deploy",
		Steps: []runtime.WorkflowStepSpec{{
			ID: "gpu",
			Operation: runtime.CommandSpec{
				Group:       "Apps",
				Use:         "deploy-gpu",
				Method:      "POST",
				PathTpl:     "/apps/gpu",
				OperationID: "Apps_DeployGPU",
			},
			When: []runtime.WorkflowCondition{
				{Value: "${input.kind}", Operator: "in", Values: []string{"gpu", "404"}},
				{Value: "${input.label}", Operator: "notin", Values: []string{""}},
			},
		}},
	}}

	if err := RenderWorkflows(specs); err != nil {
		t.Fatalf("RenderWorkflows: %v", err)
	}
	got := generatedWorkflows(t)
	for _, want := range []string{
		`When: []runtime.WorkflowCondition{`,
		`Value: "${input.kind}"`,
		`Operator: "in"`,
		`Values: []string{"gpu", "404"}`,
		`Operator: "notin"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
