package normalize

import (
	"maps"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func runtimeSchema(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool) *runtime.SchemaSpec {
	if s == nil {
		return nil
	}
	if s.Ref != "" && !visited[s.Ref] {
		if resolved := rawir.Resolve(s, defs); resolved != nil {
			next := maps.Clone(visited)
			next[s.Ref] = true
			base := runtimeSchema(resolved, defs, next)
			if rawSchemaHasRefSiblings(s) {
				sibling := *s
				sibling.Ref = ""
				return &runtime.SchemaSpec{AllOf: []*runtime.SchemaSpec{base, runtimeSchema(&sibling, defs, visited)}}
			}
			base.Nullable = base.Nullable || s.Nullable
			if s.Description != "" {
				base.Description = s.Description
			}
			return base
		}
	}
	out := &runtime.SchemaSpec{Ref: s.Ref, Type: s.Type, Description: s.Description, Format: s.Format, Nullable: s.Nullable, Enum: append([]string(nil), s.Enum...)}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*runtime.SchemaSpec, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = runtimeSchema(v, defs, visited)
		}
	}
	if len(s.Required) > 0 {
		out.Required = append([]string(nil), s.Required...)
	}
	out.Items = runtimeSchema(s.Items, defs, visited)
	out.AnyOf = runtimeSchemas(s.AnyOf, defs, visited)
	out.OneOf = runtimeSchemas(s.OneOf, defs, visited)
	out.AllOf = runtimeSchemas(s.AllOf, defs, visited)
	if s.AdditionalProperties != nil {
		out.AdditionalProperties = &runtime.AdditionalPropertiesSpec{
			Allowed: s.AdditionalProperties.Allowed,
			Schema:  runtimeSchema(s.AdditionalProperties.Schema, defs, visited),
		}
	}
	return out
}

func rawSchemaHasRefSiblings(s *rawir.RawSchema) bool {
	return s.Type != "" || s.Format != "" || len(s.Enum) > 0 || len(s.Properties) > 0 || len(s.Required) > 0 || s.Items != nil || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || len(s.AllOf) > 0 || s.AdditionalProperties != nil
}

func runtimeSchemas(schemas []*rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool) []*runtime.SchemaSpec {
	if len(schemas) == 0 {
		return nil
	}
	out := make([]*runtime.SchemaSpec, len(schemas))
	for i, schema := range schemas {
		out[i] = runtimeSchema(schema, defs, visited)
	}
	return out
}
