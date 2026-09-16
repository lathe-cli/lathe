package graphql

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func Parse(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	if src.GraphQL == nil {
		return nil, fmt.Errorf("graphql backend requires a graphql config block")
	}
	if src.GraphQL.Expose == nil {
		return nil, fmt.Errorf("graphql backend requires an expose policy")
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

	g := &generator{schema: schema, module: src.Name, config: src.GraphQL}
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
	config *sourceconfig.GraphQLConfig
}

func (g *generator) operation(opType string, field *ast.FieldDefinition) (rawir.RawOperation, error) {
	var varDefs, argList []string
	var params []rawir.RawParameter
	variableDefaults := map[string]any{}
	variablesSchema := &rawir.RawSchema{Type: "object", Properties: map[string]*rawir.RawSchema{}}
	for _, arg := range field.Arguments {
		varDefs = append(varDefs, "$"+arg.Name+": "+arg.Type.String())
		argList = append(argList, arg.Name+": $"+arg.Name)
		argSchema := g.variableSchema(arg.Type, map[string]bool{})
		if argSchema != nil {
			variablesSchema.Properties[arg.Name] = argSchema
			if arg.Type.NonNull && arg.DefaultValue == nil {
				variablesSchema.Required = append(variablesSchema.Required, arg.Name)
			}
		}
		params = append(params, g.variableParamsForArg(arg, variableDefaults)...)
	}

	sel, err := g.selectionSet(field.Type.Name(), 1, map[string]bool{})
	if err != nil {
		return rawir.RawOperation{}, fmt.Errorf("%s %q: %w", opType, field.Name, err)
	}
	group, err := g.groupFor(field.Name)
	if err != nil {
		return rawir.RawOperation{}, err
	}
	output, err := g.outputFor(field)
	if err != nil {
		return rawir.RawOperation{}, err
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

	template, err := json.Marshal(map[string]any{"query": doc.String(), "variables": variableDefaults})
	if err != nil {
		return rawir.RawOperation{}, err
	}
	return rawir.RawOperation{
		Group:       group,
		OperationID: g.module + "_" + field.Name,
		Summary:     field.Description,
		Method:      "POST",
		Path:        "/graphql",
		Parameters:  params,
		RequestBody: &rawir.RawRequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema:    variablesSchema,
			Template:  string(template),
			MergePath: "variables",
		},
		Output: output,
	}, nil
}
