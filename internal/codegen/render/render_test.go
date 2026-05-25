package render

import (
	"os"
	"path/filepath"
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
			Aliases: []string{"addon-install"},
			Short:   "OVERLAY SHORT",
			Long:    "OVERLAY LONG DESC",
			Example: "myctl demo install-addon --name foo",
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
			},
		},
	}
	overrides := map[string]overlay.Override{
		"list-users": {
			Params: map[string]overlay.ParamOverride{
				"status": {Flag: "user-status", Help: "override help", Default: "active"},
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
