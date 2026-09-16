package render

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func containsGo(source, fragment string) bool {
	tokens := func(source string) string {
		var scanner scanner.Scanner
		scanner.Init(token.NewFileSet().AddFile("", -1, len(source)), []byte(source), nil, 0)
		var parts []string
		for {
			_, tok, literal := scanner.Scan()
			if tok == token.EOF {
				break
			}
			if tok == token.SEMICOLON {
				continue
			}
			if tok == token.RBRACE && len(parts) > 0 && parts[len(parts)-1] == "," {
				parts = parts[:len(parts)-1]
			}
			if literal == "" {
				literal = tok.String()
			}
			parts = append(parts, literal)
		}
		return strings.Join(parts, "\x00")
	}
	return strings.Contains(source, fragment) || strings.Contains(tokens(source), tokens(fragment))
}

func chdirWithGoMod(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	testutil.NoError(t, os.WriteFile("go.mod", []byte("module example.com/fake\n\ngo 1.25\n"), 0o644))
}

func chdirWithGeneratedRoot(t *testing.T) {
	t.Helper()
	chdirWithGoMod(t)
	testutil.NoError(t, os.MkdirAll("internal/generated", 0o755))
}

func generatedFile(t *testing.T, path string) string {
	t.Helper()
	out, err := os.ReadFile(filepath.Join("internal/generated", path))
	testutil.Require(t, err == nil, "%v", err)
	return string(out)
}

func generatedModule(t *testing.T, name string) string {
	t.Helper()
	return generatedFile(t, filepath.Join(name, name+"_gen.go"))
}

func TestRenderModule_AppliesOverlay(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/api/v1/addon", Params: []runtime.ParamSpec{{Name: "workspace_id", Flag: "workspace-id", In: runtime.InQuery, GoType: "string"}}, RequestBody: &runtime.RequestBody{Required: true, Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string", Description: "Addon name", Format: "password", Enum: []string{"primary"}}}}}, Output: runtime.OutputHints{DefaultColumns: []string{"id"}}},
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
			Mutation:      "read",
			SearchTerms:   []string{"spend", "cost"},
			Params:        map[string]overlay.ParamOverride{"workspace_id": {Context: "workspace"}},
			Context:       &overlay.ContextOverride{SetOnSuccess: &overlay.ContextSetOnSuccess{Name: "workspace", FromParam: "workspace_id"}},
			Output: &overlay.OutputOverride{
				DefaultColumns: []string{"name", "spendMicro"},
				ColumnLabels:   map[string]string{"name": "Addon"},
				ColumnFormats: map[string]overlay.ColumnFormatOverride{
					"spendMicro": {Kind: "currency", Currency: "USD", SourceScale: 6, Grouping: true, MinFractionDigits: 2, MaxFractionDigits: 6},
				},
				ColumnAlignments: map[string]string{"spendMicro": "right"},
			},
		},
	}

	testutil.NoError(t, RenderModule("demo", "", specs, overrides))
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
		`DefaultColumns: []string{"name", "spendMicro"}`,
		`ColumnLabels: map[string]string{"name": "Addon"}`,
		`ColumnFormats: map[string]runtime.ColumnFormat{"spendMicro": runtime.ColumnFormat{Kind: "currency", Currency: "USD", SourceScale: 6, Grouping: true, MinFractionDigits: 2, MaxFractionDigits: 6}}`,
		`ColumnAlignments: map[string]string{"spendMicro": "right"}`,
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
		`Description: "Addon name"`,
		`Format: "password"`,
		`Enum: []string{"primary"}`,
	} {
		testutil.Check(t, containsGo(got, want), "output missing %q", want)
	}
	testutil.Check(t, !containsGo(got, `"raw short"`), "overlay did not replace Short; raw value leaked into output")
	flat := got
	testutil.Check(t, containsGo(flat, `Mutation: "read"`), "output missing mutation override literal")
	testutil.Check(t, containsGo(flat, `SetContext: &runtime.ContextSetHint{Name: "workspace", Param: "workspace_id"}`), "output missing set-context literal")
	testutil.Check(t, containsGo(flat, `SearchTerms: []string{ "spend", "cost", }`) || containsGo(flat, `SearchTerms: []string{"spend", "cost"}`) || containsGo(flat, `SearchTerms: []string{"spend","cost",}`), "output missing search terms literal:\n%s", flat)
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

	testutil.NoError(t, RenderModule("demo", "", specs, nil))
	got := generatedModule(t, "demo")

	for _, want := range []string{
		`Template:`,
		`createApp(name:$name)`,
		`MergePath: "variables"`,
		`Required: []string{`,
		`"input"`,
		`"name"`,
	} {
		testutil.Check(t, containsGo(got, want), "output missing %q", want)
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
	testutil.NoError(t, RenderModule("demo", "", specs, overrides))
	got := generatedModule(t, "demo")
	testutil.Check(t, containsGo(got, `"install-addon"`), "install-addon should be present")
	testutil.Check(t, !containsGo(got, `"delete-addon"`), "delete-addon should be ignored")
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
	testutil.NoError(t, ValidateOverlayModule(specs, mod))
	merged := mustMergeOverlayModule(t, specs, mod)
	testutil.Require(t, merged[0].GroupShort == "Inspect inventory items", "group short = %q", merged[0].GroupShort)
	testutil.NoError(t, renderModuleSpecs("demo", "demo", merged))
	got := generatedModule(t, "demo")
	testutil.Check(t, !containsGo(got, `"Default"`), "group should be overridden; Default should not appear")
	testutil.Check(t, containsGo(got, `"Items"`), "group should be overridden to Items")
	testutil.Check(t, containsGo(got, `GroupShort: "Inspect inventory items"`), "group short should be generated")
	testutil.Check(t, containsGo(got, "Hidden:"), "hidden should be set")
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
	merged := mustMergeOverlay(t, specs, overrides)
	testutil.Require(t, merged[0].Params[0].Argument == "state", "merged positional mapping = %#v", merged[0].Params[0])
	testutil.NoError(t, RenderModule("demo", "", specs, overrides))
	got := generatedModule(t, "demo")
	testutil.Check(t, containsGo(got, `"user-status"`), "flag should be renamed to user-status")
	testutil.Check(t, containsGo(got, `"override help"`), "help should be overridden")
	testutil.Check(t, containsGo(got, `Default: "active"`), "default should be set to active")
	testutil.Check(t, !containsGo(got, `"original help"`), "original help should not appear")
	testutil.Check(t, strings.Count(got, `Deprecated: true`) == 2, "deprecated and legacy hidden alias should both mark params deprecated; output:\n%s", got)
	testutil.Check(t, containsGo(got, `Argument: "state"`), "positional mapping should be generated; output:\n%s", got)
}

func TestRenderModule_NilOverrides(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{
		{Group: "Addon", Use: "install-addon", Short: "raw short", Method: "POST", PathTpl: "/x"},
	}
	testutil.NoError(t, RenderModule("demo", "", specs, nil))
	testutil.Check(t, containsGo(generatedModule(t, "demo"), `"raw short"`), "expected raw short preserved when overrides is nil")
}

func TestRenderModulesGen_PropagatesMountErrors(t *testing.T) {
	chdirWithGeneratedRoot(t)

	testutil.NoError(t, RenderModulesGen([]ModuleMount{{Name: "alpha"}, {Name: "beta"}}))
	got := generatedFile(t, "modules_gen.go")
	for _, want := range []string{
		`func MountModules(root *cobra.Command) error`,
		`if err := alpha.Mount(root); err != nil`,
		`if err := beta.Mount(root); err != nil`,
		`return err`,
		`return nil`,
	} {
		testutil.Check(t, containsGo(got, want), "output missing %q", want)
	}
}

func TestRenderModulesGen_UsesFlatMount(t *testing.T) {
	chdirWithGeneratedRoot(t)

	testutil.NoError(t, RenderModulesGen([]ModuleMount{{Name: "alpha", Flat: true}}))
	if got := generatedFile(t, "modules_gen.go"); !containsGo(got, `if err := alpha.MountFlat(root); err != nil`) {
		t.Fatalf("output did not use MountFlat:\n%s", got)
	}
}

func TestRenderModulesGen_WithSkillBundle(t *testing.T) {
	chdirWithGeneratedRoot(t)

	testutil.NoError(t, RenderModulesGenWithOptions([]ModuleMount{{Name: "alpha"}}, ModulesGenOptions{
		SkillBundle: &SkillBundleMount{Root: "acmectl"},
	}))
	got := generatedFile(t, "modules_gen.go")
	for _, want := range []string{
		`func Mount(root *cobra.Command) error`,
		`return MountModules(root)`,
		`lathekitup "github.com/lathe-cli/kitup/go"`,
		`lathekitupcobra "github.com/lathe-cli/kitup/go-cobra"`,
		`latheruntime.AttachCapability(root, latheruntime.CapabilitySkillBundle)`,
		`lathegeneratedskillbundle "example.com/fake/internal/generated/skillbundle"`,
	} {
		testutil.Check(t, containsGo(got, want), "output missing %q\n%s", want, got)
	}
}

func TestRenderSkillBundlePackage(t *testing.T) {
	chdirWithGeneratedRoot(t)
	testutil.NoError(t, os.MkdirAll("skills/acmectl/agents", 0o755))
	testutil.NoError(t, os.WriteFile("skills/acmectl/SKILL.md", []byte("---\nname: acmectl\ndescription: test\n---\n"), 0o644))
	testutil.NoError(t, os.WriteFile("skills/acmectl/.lathe-skill", []byte("owner"), 0o644))
	testutil.NoError(t, os.WriteFile("skills/acmectl/agents/openai.yaml", []byte("version: 1\n"), 0o644))
	testutil.NoError(t, os.MkdirAll("internal/generated/skillbundle/otherctl", 0o755))
	testutil.NoError(t, os.WriteFile("internal/generated/skillbundle/otherctl/SKILL.md", []byte("other"), 0o644))

	testutil.NoError(t, RenderSkillBundlePackage("skills/acmectl", "acmectl"))
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
	testutil.Require(t, err == nil, "%v", err)
	testutil.Require(t, strings.Contains(string(got), `//go:embed acmectl/**`), "embed bridge missing root:\n%s", got)
}
