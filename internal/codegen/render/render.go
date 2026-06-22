package render

import (
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const (
	GeneratedRoot  = "internal/generated"
	ModulesGenFile = "internal/generated/modules_gen.go"
)

type moduleCtx struct {
	Module        string
	CLIName       string
	RuntimePkg    string
	SchemaVersion int
	Ops           []runtime.CommandSpec
}

type ModuleMount struct {
	Name string
	Flat bool
}

// RuntimePkg is the import path downstream-generated modules use to reach
// lathe's runtime. Downstream forks import lathe as a library; they do not
// vendor or copy the runtime package into their own tree.
const RuntimePkg = "github.com/lathe-cli/lathe/pkg/runtime"

// RenderModule emits internal/generated/<name>/<name>_gen.go. overrides is
// keyed by command Use string; each override's non-empty fields are baked into
// the corresponding CommandSpec (Aliases are appended, not replaced). Passing
// a nil map means "no overlay", equivalent to the pre-overlay behavior.
// cliName is the name used in runtime.Build (the CLI-visible module path
// segment); if empty it falls back to name.
func RenderModule(name, cliName string, specs []runtime.CommandSpec, overrides map[string]overlay.Override) error {
	if cliName == "" {
		cliName = name
	}
	merged := MergeOverlayModule(specs, overlay.Module{Commands: overrides})
	return renderModuleSpecs(name, cliName, merged)
}

func ResolveFlatCommandPath(policy string, moduleCount int, specs []runtime.CommandSpec) (bool, error) {
	if policy == "" {
		policy = config.CommandPathAuto
	}
	if moduleCount != 1 {
		if policy == config.CommandPathFlat {
			return false, fmt.Errorf("cli.command_path=flat requires exactly one source module")
		}
		return false, nil
	}
	switch policy {
	case config.CommandPathNamespaced:
		return false, nil
	case config.CommandPathFlat:
		if conflict, ok := flatPathConflict(specs); ok {
			return false, fmt.Errorf("cli.command_path=flat conflicts with command %q", conflict)
		}
		return true, nil
	case config.CommandPathAuto:
		_, conflict := flatPathConflict(specs)
		return !conflict, nil
	default:
		return false, fmt.Errorf("unknown cli.command_path %q", policy)
	}
}

func RewriteCommandExamples(cli, module string, specs []runtime.CommandSpec, flat bool) []runtime.CommandSpec {
	if cli == "" {
		return specs
	}
	rewritten := make([]runtime.CommandSpec, 0, len(specs))
	for _, spec := range specs {
		next := cloneCommandSpec(spec)
		if next.Example != "" {
			next.Example = commandExample(next.Example, cli, module, next, flat)
		}
		rewritten = append(rewritten, next)
	}
	return rewritten
}

func flatPathConflict(specs []runtime.CommandSpec) (string, bool) {
	seen := map[string]string{}
	for _, spec := range specs {
		name := rootCommandName(spec.Group)
		if name == "" {
			continue
		}
		if reservedRootCommands[name] {
			return name, true
		}
		if group, ok := seen[name]; ok && group != spec.Group {
			return name, true
		}
		seen[name] = spec.Group
	}
	return "", false
}

func rootCommandName(use string) string {
	fields := strings.Fields(strings.ToLower(use))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

var reservedRootCommands = map[string]bool{
	"auth":       true,
	"commands":   true,
	"completion": true,
	"help":       true,
	"search":     true,
	"version":    true,
}

func MergeOverlay(specs []runtime.CommandSpec, overrides map[string]overlay.Override) []runtime.CommandSpec {
	return MergeOverlayModule(specs, overlay.Module{Commands: overrides})
}

func MergeOverlayModule(specs []runtime.CommandSpec, mod overlay.Module) []runtime.CommandSpec {
	var merged []runtime.CommandSpec
	for _, s := range specs {
		o, ok := mod.Commands[s.Use]
		if ok && o.Ignore {
			continue
		}
		cs := cloneCommandSpec(s)
		applyBulkDefaults(&cs, mod.Defaults)
		if !ok {
			merged = append(merged, cs)
			continue
		}
		applyCommandOverride(&cs, o)
		merged = append(merged, cs)
	}
	return merged
}

func cloneCommandSpec(spec runtime.CommandSpec) runtime.CommandSpec {
	cloned := spec
	cloned.Aliases = append([]string(nil), spec.Aliases...)
	cloned.Notes = append([]string(nil), spec.Notes...)
	cloned.Prerequisites = append([]string(nil), spec.Prerequisites...)
	cloned.KnownErrors = append([]runtime.KnownError(nil), spec.KnownErrors...)
	cloned.Params = append([]runtime.ParamSpec(nil), spec.Params...)
	for i := range cloned.Params {
		cloned.Params[i].Enum = append([]string(nil), spec.Params[i].Enum...)
	}
	return cloned
}

func applyBulkDefaults(spec *runtime.CommandSpec, defaults overlay.Defaults) {
	if defaults.Pagination == nil || !matchesAny(defaults.Pagination.MatchCommands, spec.Use) {
		return
	}
	for i := range spec.Params {
		if spec.Params[i].Default != "" {
			continue
		}
		if value, ok := defaults.Pagination.Params[spec.Params[i].Name]; ok {
			spec.Params[i].Default = value
		}
	}
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		matched, err := path.Match(pattern, value)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func applyCommandOverride(spec *runtime.CommandSpec, override overlay.Override) {
	if override.Short != "" {
		spec.Short = override.Short
	}
	if override.Long != "" {
		spec.Long = override.Long
	}
	if override.Example != "" {
		spec.Example = override.Example
	}
	if len(override.Notes) > 0 {
		spec.Notes = append([]string(nil), override.Notes...)
	}
	if len(override.Prerequisites) > 0 {
		spec.Prerequisites = append([]string(nil), override.Prerequisites...)
	}
	if len(override.KnownErrors) > 0 {
		spec.KnownErrors = make([]runtime.KnownError, 0, len(override.KnownErrors))
		for _, ke := range override.KnownErrors {
			spec.KnownErrors = append(spec.KnownErrors, runtime.KnownError{Status: ke.Status, Cause: ke.Cause})
		}
	}
	if len(override.Aliases) > 0 {
		spec.Aliases = append(spec.Aliases, override.Aliases...)
	}
	if override.Group != "" {
		spec.Group = override.Group
	}
	if override.Hidden != nil {
		spec.Hidden = *override.Hidden
	}
	if len(override.Params) > 0 {
		for j := range spec.Params {
			po, pok := override.Params[spec.Params[j].Name]
			if !pok {
				continue
			}
			if po.Flag != "" {
				spec.Params[j].Flag = po.Flag
			}
			if po.Help != "" {
				spec.Params[j].Help = po.Help
			}
			if po.Required {
				spec.Params[j].Required = true
			}
			if po.Default != "" {
				spec.Params[j].Default = po.Default
			}
			if po.Deprecated || po.DeprecatedAlias {
				spec.Params[j].Deprecated = true
			}
		}
	}
}

func renderModuleSpecs(name, cliName string, specs []runtime.CommandSpec) error {
	var buf strings.Builder
	ctx := moduleCtx{
		Module:        name,
		CLIName:       cliName,
		RuntimePkg:    RuntimePkg,
		SchemaVersion: runtime.SchemaVersion,
		Ops:           specs,
	}
	if err := moduleTmpl.Execute(&buf, ctx); err != nil {
		return err
	}
	outPath := filepath.Join(GeneratedRoot, name, name+"_gen.go")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(outPath+".unformatted", []byte(buf.String()), 0o644)
		return err
	}
	if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d commands\n", outPath, len(specs))
	return nil
}

func RenderModulesGen(modules []ModuleMount) error {
	mp, err := modulePath()
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := modulesTmpl.Execute(&buf, struct {
		Prefix  string
		Modules []ModuleMount
	}{Prefix: mp + "/internal/generated/", Modules: modules}); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(ModulesGenFile+".unformatted", []byte(buf.String()), 0o644)
		return err
	}
	if err := os.WriteFile(ModulesGenFile, formatted, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d modules\n", ModulesGenFile, len(modules))
	return nil
}

func schemaLiteral(s *runtime.SchemaSpec) string {
	if s == nil {
		return "nil"
	}
	var b strings.Builder
	writeSchemaLiteral(&b, s)
	return b.String()
}

func writeSchemaLiteral(b *strings.Builder, s *runtime.SchemaSpec) {
	if s == nil {
		b.WriteString("nil")
		return
	}
	b.WriteString("&runtime.SchemaSpec{")
	if s.Ref != "" {
		fmt.Fprintf(b, "Ref: %q,", s.Ref)
	}
	if s.Type != "" {
		fmt.Fprintf(b, "Type: %q,", s.Type)
	}
	if len(s.Properties) > 0 {
		b.WriteString("Properties: map[string]*runtime.SchemaSpec{")
		keys := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "%q: ", k)
			writeSchemaLiteral(b, s.Properties[k])
			b.WriteByte(',')
		}
		b.WriteString("},")
	}
	if s.Items != nil {
		b.WriteString("Items: ")
		writeSchemaLiteral(b, s.Items)
		b.WriteByte(',')
	}
	b.WriteByte('}')
}

var moduleTmpl = template.Must(template.New("gen").Funcs(template.FuncMap{
	"schemaLiteral": schemaLiteral,
}).Parse(`// Code generated by lathe codegen. DO NOT EDIT.

package {{.Module}}

import (
	"github.com/spf13/cobra"

	"{{.RuntimePkg}}"
)

const generatedSchemaVersion = {{.SchemaVersion}}

func Mount(root *cobra.Command) error {
	if err := runtime.AssertSchema(generatedSchemaVersion); err != nil {
		return err
	}
	runtime.Build(root, {{printf "%q" .CLIName}}, Specs)
	return nil
}

func MountFlat(root *cobra.Command) error {
	if err := runtime.AssertSchema(generatedSchemaVersion); err != nil {
		return err
	}
	return runtime.BuildFlat(root, {{printf "%q" .CLIName}}, Specs)
}

var Specs = []runtime.CommandSpec{
{{- range $op := .Ops}}
	{
		Group:       {{printf "%q" $op.Group}},
		Use:         {{printf "%q" $op.Use}},
		{{- if $op.Aliases}}
		Aliases:     []string{ {{- range $op.Aliases}}{{printf "%q" .}}, {{end -}} },
		{{- end}}
		Short:       {{printf "%q" $op.Short}},
		{{- if $op.Long}}
		Long:        {{printf "%q" $op.Long}},
		{{- end}}
		{{- if $op.Example}}
		Example:     {{printf "%q" $op.Example}},
		{{- end}}
		{{- if $op.Notes}}
		Notes: []string{
			{{- range $op.Notes}}{{printf "%q" .}},{{end}}
		},
		{{- end}}
		{{- if $op.Prerequisites}}
		Prerequisites: []string{
			{{- range $op.Prerequisites}}{{printf "%q" .}},{{end}}
		},
		{{- end}}
		{{- if $op.KnownErrors}}
		KnownErrors: []runtime.KnownError{
			{{- range $op.KnownErrors}}
			{Status: {{.Status}}, Cause: {{printf "%q" .Cause}}},
			{{- end}}
		},
		{{- end}}
		{{- if $op.OperationID}}
		OperationID: {{printf "%q" $op.OperationID}},
		{{- end}}
		{{- if $op.Hidden}}
		Hidden:      true,
		{{- end}}
		{{- if $op.Deprecated}}
		Deprecated:  true,
		{{- end}}
		Method:      {{printf "%q" $op.Method}},
		PathTpl:     {{printf "%q" $op.PathTpl}},
		{{- if $op.Params}}
		Params: []runtime.ParamSpec{
			{{- range $op.Params}}
			{Name: {{printf "%q" .Name}}, Flag: {{printf "%q" .Flag}}, In: {{printf "%q" .In}}, GoType: {{printf "%q" .GoType}}, Help: {{printf "%q" .Help}}, Required: {{.Required}}{{- if .Default}}, Default: {{printf "%q" .Default}}{{end}}{{- if .Enum}}, Enum: []string{ {{- range .Enum}}{{printf "%q" .}}, {{end -}} }{{end}}{{- if .Format}}, Format: {{printf "%q" .Format}}{{end}}{{- if .Deprecated}}, Deprecated: true{{end}}},
			{{- end}}
		},
		{{- end}}
		{{- if $op.RequestBody}}
			RequestBody: &runtime.RequestBody{
				Required: {{$op.RequestBody.Required}},
				{{- if $op.RequestBody.MediaType}}
				MediaType: {{printf "%q" $op.RequestBody.MediaType}},
				{{- end}}
				{{- if $op.RequestBody.Schema}}
				Schema: {{schemaLiteral $op.RequestBody.Schema}},
				{{- end}}
				{{- if $op.RequestBody.Template}}
				Template: {{printf "%q" $op.RequestBody.Template}},
				{{- end}}
				{{- if $op.RequestBody.MergePath}}
				MergePath: {{printf "%q" $op.RequestBody.MergePath}},
				{{- end}}
			},
			{{- end}}
		{{- if or $op.Output.ListPath $op.Output.DefaultColumns $op.Output.ResponseMediaType $op.Output.Pagination $op.Output.Streaming}}
		Output: runtime.OutputHints{
			{{- if $op.Output.ListPath}}ListPath: {{printf "%q" $op.Output.ListPath}},{{end}}
			{{- if $op.Output.DefaultColumns}}DefaultColumns: []string{
				{{- range $op.Output.DefaultColumns}}{{printf "%q" .}},{{end}}
			},{{end}}
			{{- if $op.Output.ResponseMediaType}}ResponseMediaType: {{printf "%q" $op.Output.ResponseMediaType}},{{end}}
			{{- if $op.Output.Pagination}}Pagination: &runtime.PaginationHint{
				Strategy: {{printf "%q" $op.Output.Pagination.Strategy}},
				{{- if $op.Output.Pagination.TokenParam}}TokenParam: {{printf "%q" $op.Output.Pagination.TokenParam}},{{end}}
				{{- if $op.Output.Pagination.TokenField}}TokenField: {{printf "%q" $op.Output.Pagination.TokenField}},{{end}}
				{{- if $op.Output.Pagination.LimitParam}}LimitParam: {{printf "%q" $op.Output.Pagination.LimitParam}},{{end}}
			},{{end}}
			{{- if $op.Output.Streaming}}Streaming: &runtime.StreamingHint{
				Strategy: {{printf "%q" $op.Output.Streaming.Strategy}},
			},{{end}}
		},
		{{- end}}
		{{- if $op.Security}}
		Security: &runtime.SecurityHint{
			{{- if $op.Security.Public}}Public: true,{{end}}
			{{- if $op.Security.Scopes}}Scopes: []string{
				{{- range $op.Security.Scopes}}{{printf "%q" .}},{{end}}
			},{{end}}
		},
		{{- end}}
	},
{{- end}}
}
`))

var modulesTmpl = template.Must(template.New("modules").Parse(`// Code generated by lathe codegen. DO NOT EDIT.

package generated

import (
	"github.com/spf13/cobra"

{{- range .Modules}}
	{{.Name}} "{{$.Prefix}}{{.Name}}"
{{- end}}
)

// MountModules mounts every module declared in sources.yaml under root.
// The import list above is the single source of truth for which modules
// are compiled into this binary. main.go wires this call after
// app.NewApp() so the framework package never imports downstream code.
func MountModules(root *cobra.Command) error {
{{- range .Modules}}
	if err := {{.Name}}.{{if .Flat}}MountFlat{{else}}Mount{{end}}(root); err != nil {
		return err
	}
{{- end}}
	return nil
}
`))

// modulePath reads the `module` directive from go.mod in the current working
// directory. Codegen uses this to compute the downstream's own generated/
// package import prefix so a downstream fork can rename its Go module without
// touching codegen source. The runtime package is always imported from lathe
// itself (see RuntimePkg).
func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module directive in go.mod")
}
