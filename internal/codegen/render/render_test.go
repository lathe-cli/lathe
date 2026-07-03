package render

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func TestRenderModule_AppliesOverlay(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/api/v1/addon", RequestBody: &runtime.RequestBody{Required: true, Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}}}}},
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
	overrides := map[string]overlay.Override{
		"get-item": {Group: "Items", Hidden: &hidden},
	}
	if err := RenderModule("demo", "", specs, overrides); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	got := generatedModule(t, "demo")
	if strings.Contains(got, `"Default"`) {
		t.Error("group should be overridden; Default should not appear")
	}
	if !strings.Contains(got, `"Items"`) {
		t.Error("group should be overridden to Items")
	}
	if !strings.Contains(got, "Hidden:") {
		t.Error("hidden should be set")
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
				"status": {Flag: "user-status", Help: "override help", Default: "active", Deprecated: true},
				"legacy": {DeprecatedAlias: true},
			},
		},
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
	for _, reserved := range []string{"__lathe", "auth", "commands", "help", "login", "search", "skill", "update"} {
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
