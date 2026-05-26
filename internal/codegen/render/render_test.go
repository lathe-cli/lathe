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

func TestRenderModule_AppliesOverlay(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/api/v1/addon", RequestBody: &runtime.RequestBody{Required: true, Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}}}}},
		{Group: "Addon", Use: "untouched", Short: "untouched short", Method: "GET", PathTpl: "/api/v1/x"},
	}
	overrides := map[string]overlay.Override{
		"install-addon": {
			Aliases:       []string{"addon-install"},
			Short:         "OVERLAY SHORT",
			Long:          "OVERLAY LONG DESC",
			Example:       "myctl demo install-addon --name foo",
			Notes:         []string{"Use the canonical addon ID."},
			Prerequisites: []string{"List clusters before installing."},
			KnownErrors:   []overlay.KnownError{{Status: 400, Cause: "missing addon name"}},
		},
	}

	if err := RenderModule("demo", "", specs, overrides); err != nil {
		t.Fatalf("RenderModule: %v", err)
	}
	out, err := os.ReadFile(filepath.Join("internal/generated/demo/demo_gen.go"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`"OVERLAY SHORT"`,
		`"OVERLAY LONG DESC"`,
		`"myctl demo install-addon --name foo"`,
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

func TestRenderModule_IgnoreDropsCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	out, err := os.ReadFile(filepath.Join("internal/generated/demo/demo_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"install-addon"`) {
		t.Error("install-addon should be present")
	}
	if strings.Contains(got, `"delete-addon"`) {
		t.Error("delete-addon should be ignored")
	}
}

func TestRenderModule_GroupAndHiddenOverride(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	out, err := os.ReadFile(filepath.Join("internal/generated/demo/demo_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
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
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	out, err := os.ReadFile(filepath.Join("internal/generated/demo/demo_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
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
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/x"},
	}
	if err := RenderModule("demo", "", specs, nil); err != nil {
		t.Fatalf("RenderModule nil overrides: %v", err)
	}
	out, err := os.ReadFile(filepath.Join("internal/generated/demo/demo_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"raw short"`) {
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
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("internal/generated", 0o755); err != nil {
		t.Fatal(err)
	}

	if err := RenderModulesGen([]string{"alpha", "beta"}); err != nil {
		t.Fatalf("RenderModulesGen: %v", err)
	}
	out, err := os.ReadFile("internal/generated/modules_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
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
