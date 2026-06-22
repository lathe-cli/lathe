package graphql

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

const maxSelectionDepth = 3

func Parse(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	if src.GraphQL == nil {
		return nil, fmt.Errorf("graphql backend requires a graphql config block")
	}
	rel := src.GraphQL.Schema
	data, err := os.ReadFile(filepath.Join(syncDir, rel))
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", rel, err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: rel, Input: string(data)})
	if err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", rel, err)
	}

	g := &generator{schema: schema, module: src.Name}
	var ops []rawir.RawOperation
	seen := map[string]bool{}
	roots := []struct {
		opType   string
		def      *ast.Definition
		patterns []string
	}{
		{"query", schema.Query, src.GraphQL.Expose.Queries},
		{"mutation", schema.Mutation, src.GraphQL.Expose.Mutations},
	}
	for _, root := range roots {
		if root.def == nil {
			continue
		}
		for _, field := range root.def.Fields {
			if strings.HasPrefix(field.Name, "__") {
				continue
			}
			matched, err := matchAny(root.patterns, field.Name)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
			op, err := g.operation(root.opType, field)
			if err != nil {
				return nil, err
			}
			if seen[op.OperationID] {
				return nil, fmt.Errorf("operation name collision: %q is exposed by more than one root type in source %q; expose only one or rename", field.Name, src.Name)
			}
			seen[op.OperationID] = true
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no operations matched the graphql.expose policy for source %q", src.Name)
	}
	return &rawir.RawModule{Name: src.Name, Operations: ops}, nil
}

type generator struct {
	schema *ast.Schema
	module string
}

func (g *generator) operation(opType string, field *ast.FieldDefinition) (rawir.RawOperation, error) {
	var varDefs, argList []string
	var params []rawir.RawParameter
	for _, arg := range field.Arguments {
		varDefs = append(varDefs, "$"+arg.Name+": "+arg.Type.String())
		argList = append(argList, arg.Name+": $"+arg.Name)
		def := g.schema.Types[arg.Type.Name()]
		if def != nil && def.IsLeafType() {
			params = append(params, rawir.RawParameter{
				Name:        arg.Name,
				In:          "variable",
				Required:    arg.Type.NonNull,
				Type:        rawType(arg.Type),
				Description: arg.Description,
			})
			continue
		}
		if arg.Type.NonNull && arg.DefaultValue == nil {
			return rawir.RawOperation{}, fmt.Errorf("%s %q: required argument %q of type %q is not a scalar and cannot be expressed as a CLI flag", opType, field.Name, arg.Name, arg.Type.String())
		}
	}

	sel, err := g.selectionSet(field.Type.Name(), 1, map[string]bool{})
	if err != nil {
		return rawir.RawOperation{}, fmt.Errorf("%s %q: %w", opType, field.Name, err)
	}

	var doc strings.Builder
	doc.WriteString(opType)
	doc.WriteString(" ")
	doc.WriteString(field.Name)
	if len(varDefs) > 0 {
		doc.WriteString("(" + strings.Join(varDefs, ", ") + ")")
	}
	doc.WriteString(" { ")
	doc.WriteString(field.Name)
	if len(argList) > 0 {
		doc.WriteString("(" + strings.Join(argList, ", ") + ")")
	}
	if sel != "" {
		doc.WriteString(" " + sel)
	}
	doc.WriteString(" }")

	template, err := json.Marshal(map[string]any{"query": doc.String(), "variables": map[string]any{}})
	if err != nil {
		return rawir.RawOperation{}, err
	}
	return rawir.RawOperation{
		Group:       g.module,
		OperationID: g.module + "_" + field.Name,
		Summary:     field.Description,
		Method:      "POST",
		Path:        "/graphql",
		Parameters:  params,
		RequestBody: &rawir.RawRequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  string(template),
			MergePath: "variables",
		},
	}, nil
}

func (g *generator) selectionSet(typeName string, depth int, onPath map[string]bool) (string, error) {
	def := g.schema.Types[typeName]
	if def == nil {
		return "", fmt.Errorf("unknown type %q", typeName)
	}
	if def.IsLeafType() {
		return "", nil
	}
	if def.Kind == ast.Union {
		return "{ __typename }", nil
	}
	var fields []string
	for _, f := range def.Fields {
		if strings.HasPrefix(f.Name, "__") || hasRequiredArgs(f) {
			continue
		}
		childName := f.Type.Name()
		child := g.schema.Types[childName]
		if child == nil {
			continue
		}
		if child.IsLeafType() {
			fields = append(fields, f.Name)
			continue
		}
		if depth >= maxSelectionDepth || onPath[childName] {
			continue
		}
		next := clonePath(onPath)
		next[typeName] = true
		sub, err := g.selectionSet(childName, depth+1, next)
		if err != nil || sub == "" {
			continue
		}
		fields = append(fields, f.Name+" "+sub)
	}
	if len(fields) == 0 {
		return "", fmt.Errorf("type %q has no selectable fields within depth %d", typeName, maxSelectionDepth)
	}
	return "{ " + strings.Join(fields, " ") + " }", nil
}

func hasRequiredArgs(f *ast.FieldDefinition) bool {
	for _, a := range f.Arguments {
		if a.Type.NonNull && a.DefaultValue == nil {
			return true
		}
	}
	return false
}

func clonePath(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func matchAny(patterns []string, name string) (bool, error) {
	for _, p := range patterns {
		ok, err := path.Match(p, name)
		if err != nil {
			return false, fmt.Errorf("invalid expose pattern %q: %w", p, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func rawType(t *ast.Type) string {
	if t.Elem != nil {
		return scalarType(t.Elem) + "-array"
	}
	return scalarType(t)
}

func scalarType(t *ast.Type) string {
	switch t.Name() {
	case "Int":
		return "integer"
	case "Float":
		return "number"
	case "Boolean":
		return "boolean"
	default:
		return "string"
	}
}
