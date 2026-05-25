package render

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/lathe-cli/lathe/internal/overlay"
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
	merged := MergeOverlay(specs, overrides)
	return renderModuleSpecs(name, cliName, merged)
}

func MergeOverlay(specs []runtime.CommandSpec, overrides map[string]overlay.Override) []runtime.CommandSpec {
	var merged []runtime.CommandSpec
	for _, s := range specs {
		o, ok := overrides[s.Use]
		if ok && o.Ignore {
			continue
		}
		cs := s
		if !ok {
			merged = append(merged, cs)
			continue
		}
		if o.Short != "" {
			cs.Short = o.Short
		}
		if o.Long != "" {
			cs.Long = o.Long
		}
		if o.Example != "" {
			cs.Example = o.Example
		}
		if len(o.Aliases) > 0 {
			cs.Aliases = append(cs.Aliases, o.Aliases...)
		}
		if o.Group != "" {
			cs.Group = o.Group
		}
		if o.Hidden != nil {
			cs.Hidden = *o.Hidden
		}
		if len(o.Params) > 0 {
			for j := range cs.Params {
				po, pok := o.Params[cs.Params[j].Name]
				if !pok {
					continue
				}
				if po.Flag != "" {
					cs.Params[j].Flag = po.Flag
				}
				if po.Help != "" {
					cs.Params[j].Help = po.Help
				}
				if po.Default != "" {
					cs.Params[j].Default = po.Default
				}
				if po.Deprecated || po.Hidden {
					cs.Params[j].Deprecated = true
				}
			}
		}
		merged = append(merged, cs)
	}
	return merged
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

func RenderModulesGen(names []string) error {
	mp, err := modulePath()
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := modulesTmpl.Execute(&buf, struct {
		Prefix  string
		Modules []string
	}{Prefix: mp + "/internal/generated/", Modules: names}); err != nil {
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
	fmt.Fprintf(os.Stderr, "wrote %s: %d modules\n", ModulesGenFile, len(names))
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
	{{.}} "{{$.Prefix}}{{.}}"
{{- end}}
)

// MountModules mounts every module declared in sources.yaml under root.
// The import list above is the single source of truth for which modules
// are compiled into this binary. main.go wires this call after
// app.NewApp() so the framework package never imports downstream code.
func MountModules(root *cobra.Command) error {
{{- range .Modules}}
	if err := {{.}}.Mount(root); err != nil {
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
