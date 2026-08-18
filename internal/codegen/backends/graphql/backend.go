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

const defaultMaxSelectionDepth = 3

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

	g := &generator{schema: schema, module: src.Name, config: src.GraphQL, schemas: map[string]*rawir.RawSchema{}}
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
	return &rawir.RawModule{Name: src.Name, Operations: ops, Schemas: g.schemas}, nil
}

type generator struct {
	schema  *ast.Schema
	module  string
	config  *sourceconfig.GraphQLConfig
	schemas map[string]*rawir.RawSchema
}

func (g *generator) operation(opType string, field *ast.FieldDefinition) (rawir.RawOperation, error) {
	var varDefs, argList []string
	var params []rawir.RawParameter
	variableDefaults := map[string]any{}
	variablesSchema := &rawir.RawSchema{Type: "object", Properties: map[string]*rawir.RawSchema{}}
	for _, arg := range field.Arguments {
		varDefs = append(varDefs, "$"+arg.Name+": "+arg.Type.String())
		argList = append(argList, arg.Name+": $"+arg.Name)
		argSchema, err := g.variableSchema(arg.Type)
		if err != nil {
			return rawir.RawOperation{}, fmt.Errorf("%s %q: argument %q schema: %w", opType, field.Name, arg.Name, err)
		}
		if argSchema != nil {
			variablesSchema.Properties[arg.Name] = argSchema
			if arg.Type.NonNull && arg.DefaultValue == nil {
				variablesSchema.Required = append(variablesSchema.Required, arg.Name)
			}
		}
		argParams, err := g.variableParamsForArg(arg, variableDefaults)
		if err != nil {
			return rawir.RawOperation{}, err
		}
		params = append(params, argParams...)
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

func (g *generator) variableParamsForArg(arg *ast.ArgumentDefinition, defaults map[string]any) ([]rawir.RawParameter, error) {
	def := g.schema.Types[arg.Type.Name()]
	if def != nil && def.IsLeafType() {
		return []rawir.RawParameter{{
			Name:        arg.Name,
			In:          "variable",
			Required:    arg.Type.NonNull,
			Type:        rawType(arg.Type),
			Description: arg.Description,
			Enum:        enumValues(arg.Type, def),
		}}, nil
	}
	if def != nil && def.Kind == ast.InputObject && arg.Type.Elem == nil {
		return g.inputObjectParams(arg.Name, arg.Type, arg.Type.NonNull && arg.DefaultValue == nil, defaults, map[string]bool{})
	}
	return nil, nil
}

func (g *generator) inputObjectParams(prefix string, typ *ast.Type, required bool, defaults map[string]any, onPath map[string]bool) ([]rawir.RawParameter, error) {
	def := g.schema.Types[typ.Name()]
	if def == nil || def.Kind != ast.InputObject || typ.Elem != nil {
		return nil, nil
	}
	if onPath[def.Name] {
		return nil, nil
	}
	if required {
		setDefaultObject(defaults, prefix)
	}

	next := clonePath(onPath)
	next[def.Name] = true
	var params []rawir.RawParameter
	for _, field := range def.Fields {
		name := prefix + "." + field.Name
		fieldRequired := required && field.Type.NonNull && field.DefaultValue == nil
		fieldDef := g.schema.Types[field.Type.Name()]
		if fieldDef != nil && fieldDef.IsLeafType() {
			params = append(params, rawir.RawParameter{
				Name:        name,
				In:          "variable",
				Required:    fieldRequired,
				Type:        rawType(field.Type),
				Description: field.Description,
				Enum:        enumValues(field.Type, fieldDef),
			})
			continue
		}
		if fieldDef != nil && fieldDef.Kind == ast.InputObject && field.Type.Elem == nil {
			child, err := g.inputObjectParams(name, field.Type, fieldRequired, defaults, next)
			if err != nil {
				return nil, err
			}
			params = append(params, child...)
			continue
		}
	}
	return params, nil
}

func (g *generator) variableSchema(typ *ast.Type) (*rawir.RawSchema, error) {
	if typ == nil {
		return nil, nil
	}
	nullable := !typ.NonNull
	if typ.Elem != nil {
		item, err := g.variableSchema(typ.Elem)
		if err != nil {
			return nil, err
		}
		return &rawir.RawSchema{Type: "array", Nullable: nullable, AcceptSingletonArray: true, Items: item}, nil
	}
	def := g.schema.Types[typ.Name()]
	if def == nil {
		return &rawir.RawSchema{Type: "string", Nullable: nullable}, nil
	}
	if def.IsLeafType() {
		if def.Kind == ast.Scalar && typ.Name() != "Int" && typ.Name() != "Float" && typ.Name() != "String" && typ.Name() != "Boolean" && typ.Name() != "ID" {
			return graphqlCustomScalarSchema(nullable), nil
		}
		return &rawir.RawSchema{Type: scalarType(typ), Nullable: nullable, AcceptIntegerID: typ.Name() == "ID"}, nil
	}
	if def.Kind != ast.InputObject {
		return &rawir.RawSchema{Type: "object", Nullable: nullable}, nil
	}
	if _, ok := g.schemas[def.Name]; !ok {
		definition := &rawir.RawSchema{
			Type:                 "object",
			Properties:           map[string]*rawir.RawSchema{},
			AdditionalProperties: &rawir.RawAdditionalProperties{Allowed: false},
		}
		g.schemas[def.Name] = definition
		for _, field := range def.Fields {
			fieldSchema, err := g.variableSchema(field.Type)
			if err != nil {
				return nil, err
			}
			if fieldSchema != nil {
				definition.Properties[field.Name] = fieldSchema
			}
			if field.Type.NonNull && field.DefaultValue == nil {
				definition.Required = append(definition.Required, field.Name)
			}
		}
	}
	return &rawir.RawSchema{Ref: rawir.RefPrefix + def.Name, Nullable: nullable}, nil
}

func graphqlCustomScalarSchema(nullable bool) *rawir.RawSchema {
	return &rawir.RawSchema{
		Nullable: nullable,
		AnyOf: []*rawir.RawSchema{
			{Type: "object"},
			{Type: "array"},
			{Type: "string"},
			{Type: "boolean"},
			{Type: "number"},
		},
	}
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
		pruned, err := g.pruned(def.Name, f.Name)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(f.Name, "__") || hasRequiredArgs(f) || pruned {
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
		if depth >= g.selectionMaxDepth() || onPath[childName] {
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
		return "", fmt.Errorf("type %q has no selectable fields within depth %d", typeName, g.selectionMaxDepth())
	}
	return "{ " + strings.Join(fields, " ") + " }", nil
}

func (g *generator) groupFor(fieldName string) (string, error) {
	var group string
	for _, rule := range g.config.Groups {
		matched, err := matchAny(rule.Match, fieldName)
		if err != nil {
			return "", err
		}
		if matched {
			if group != "" {
				return "", fmt.Errorf("operation %q matches multiple graphql.groups rules", fieldName)
			}
			group = rule.Group
		}
	}
	if group != "" {
		return group, nil
	}
	return g.module, nil
}

func (g *generator) outputFor(field *ast.FieldDefinition) (*rawir.RawOutputHints, error) {
	fieldName := field.Name
	var output *rawir.RawOutputHints
	for _, rule := range g.config.Output {
		matched, err := matchAny(rule.Match, fieldName)
		if err != nil {
			return nil, err
		}
		if matched {
			if output != nil {
				return nil, fmt.Errorf("operation %q matches multiple graphql.output rules", fieldName)
			}
			output = &rawir.RawOutputHints{
				ListPath:       rule.ListPath,
				DefaultColumns: append([]string(nil), rule.DefaultColumns...),
			}
		}
	}
	if output != nil {
		pagination, err := g.relayPaginationFor(field, output.ListPath)
		if err != nil {
			return nil, err
		}
		output.Pagination = pagination
	}
	return output, nil
}

func (g *generator) relayPaginationFor(field *ast.FieldDefinition, listPath string) (*rawir.RawPaginationHint, error) {
	if listPath == "" {
		return nil, nil
	}
	wantPrefix := "data." + field.Name + "."
	if listPath != wantPrefix+"nodes" && listPath != wantPrefix+"edges" {
		return nil, nil
	}
	if !hasArg(field, "after") {
		return nil, nil
	}
	if g.selectionMaxDepth() <= 1 {
		return nil, nil
	}
	connection := g.schema.Types[field.Type.Name()]
	if connection == nil {
		return nil, nil
	}
	pruned, err := g.pruned(connection.Name, "pageInfo")
	if err != nil || pruned {
		return nil, err
	}
	pageInfoField := fieldByName(connection.Fields, "pageInfo")
	if pageInfoField == nil {
		return nil, nil
	}
	pageInfo := g.schema.Types[pageInfoField.Type.Name()]
	if pageInfo == nil {
		return nil, nil
	}
	for _, fieldName := range []string{"endCursor", "hasNextPage"} {
		pruned, err := g.pruned(pageInfo.Name, fieldName)
		if err != nil || pruned || fieldByName(pageInfo.Fields, fieldName) == nil {
			return nil, err
		}
	}
	limitParam := ""
	if hasArg(field, "first") {
		limitParam = "variables.first"
	}
	return &rawir.RawPaginationHint{
		Strategy:   "body-cursor",
		TokenParam: "variables.after",
		TokenField: wantPrefix + "pageInfo.endCursor",
		LimitParam: limitParam,
	}, nil
}

func (g *generator) selectionMaxDepth() int {
	if g.config.Selection != nil && g.config.Selection.MaxDepth != nil {
		return *g.config.Selection.MaxDepth
	}
	return defaultMaxSelectionDepth
}

func (g *generator) pruned(typeName string, fieldName string) (bool, error) {
	if g.config.Selection == nil {
		return false, nil
	}
	target := typeName + "." + fieldName
	for _, pattern := range g.config.Selection.Prune {
		matched, err := path.Match(pattern, target)
		if err != nil {
			return false, fmt.Errorf("invalid graphql.selection.prune pattern %q: %w", pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
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

func fieldByName(fields ast.FieldList, name string) *ast.FieldDefinition {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func hasArg(field *ast.FieldDefinition, name string) bool {
	for _, arg := range field.Arguments {
		if arg.Name == name {
			return true
		}
	}
	return false
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

func enumValues(typ *ast.Type, def *ast.Definition) []string {
	if typ.Elem != nil || def == nil || def.Kind != ast.Enum {
		return nil
	}
	values := make([]string, 0, len(def.EnumValues))
	for _, v := range def.EnumValues {
		values = append(values, v.Name)
	}
	return values
}

func setDefaultObject(root map[string]any, path string) {
	current := root
	parts := strings.Split(path, ".")
	for _, part := range parts {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
}
