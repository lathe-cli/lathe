package graphql

import (
	"maps"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/vektah/gqlparser/v2/ast"
)

func (g *generator) variableParamsForArg(arg *ast.ArgumentDefinition, defaults map[string]any) []rawir.RawParameter {
	def := g.schema.Types[arg.Type.Name()]
	if def != nil && def.IsLeafType() {
		return []rawir.RawParameter{{
			Name:        arg.Name,
			In:          "variable",
			Required:    arg.Type.NonNull,
			Type:        rawType(arg.Type),
			Description: arg.Description,
			Enum:        enumValues(arg.Type, def),
		}}
	}
	if def != nil && def.Kind == ast.InputObject && arg.Type.Elem == nil {
		return g.inputObjectParams(arg.Name, arg.Type, arg.Type.NonNull && arg.DefaultValue == nil, defaults, map[string]bool{})
	}
	return nil
}

func (g *generator) inputObjectParams(prefix string, typ *ast.Type, required bool, defaults map[string]any, onPath map[string]bool) []rawir.RawParameter {
	def := g.schema.Types[typ.Name()]
	if def == nil || def.Kind != ast.InputObject || typ.Elem != nil {
		return nil
	}
	if onPath[def.Name] {
		return nil
	}
	if required {
		setDefaultObject(defaults, prefix)
	}

	next := maps.Clone(onPath)
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
			params = append(params, g.inputObjectParams(name, field.Type, fieldRequired, defaults, next)...)
		}
	}
	return params
}

func (g *generator) variableSchema(typ *ast.Type, onPath map[string]bool) *rawir.RawSchema {
	if typ == nil {
		return nil
	}
	if typ.Elem != nil {
		return &rawir.RawSchema{Type: "array", Items: g.variableSchema(typ.Elem, onPath)}
	}
	def := g.schema.Types[typ.Name()]
	if def == nil {
		return &rawir.RawSchema{Type: "string"}
	}
	if def.IsLeafType() {
		return &rawir.RawSchema{Type: scalarType(typ)}
	}
	if def.Kind != ast.InputObject {
		return &rawir.RawSchema{Type: "object"}
	}
	if onPath[def.Name] {
		return &rawir.RawSchema{Type: "object"}
	}

	next := maps.Clone(onPath)
	next[def.Name] = true
	schema := &rawir.RawSchema{Type: "object", Properties: map[string]*rawir.RawSchema{}}
	for _, field := range def.Fields {
		fieldSchema := g.variableSchema(field.Type, next)
		if fieldSchema != nil {
			schema.Properties[field.Name] = fieldSchema
		}
		if field.Type.NonNull && field.DefaultValue == nil {
			schema.Required = append(schema.Required, field.Name)
		}
	}
	return schema
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
