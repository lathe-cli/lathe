package normalize

import (
	"maps"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func runtimeSchema(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool, request bool) *runtime.SchemaSpec {
	if s == nil {
		return nil
	}
	if s.Ref != "" && !visited[s.Ref] {
		if resolved := rawir.Resolve(s, defs); resolved != nil {
			next := maps.Clone(visited)
			next[s.Ref] = true
			base := runtimeSchema(resolved, defs, next, request)
			if rawSchemaHasRefSiblings(s) {
				sibling := *s
				sibling.Ref = ""
				base = &runtime.SchemaSpec{AllOf: []*runtime.SchemaSpec{base, runtimeSchema(&sibling, defs, visited, request)}}
			} else {
				base.Nullable = base.Nullable || s.Nullable
				if s.Description != "" {
					base.Description = s.Description
				}
			}
			if request {
				readOnly := map[string]bool{}
				collectReadOnlyProperties(s, defs, map[string]bool{}, readOnly)
				removeReadOnlyRequired(base, readOnly)
			}
			return base
		}
	}
	out := &runtime.SchemaSpec{Ref: s.Ref, Type: s.Type, Description: s.Description, Format: s.Format, Nullable: s.Nullable, Enum: append([]string(nil), s.Enum...)}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*runtime.SchemaSpec, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = runtimeSchema(v, defs, visited, request)
		}
	}
	out.Required = append([]string(nil), s.Required...)
	out.Items = runtimeSchema(s.Items, defs, visited, request)
	out.AnyOf = runtimeSchemas(s.AnyOf, defs, visited, request)
	out.OneOf = runtimeSchemas(s.OneOf, defs, visited, request)
	out.AllOf = runtimeSchemas(s.AllOf, defs, visited, request)
	if s.AdditionalProperties != nil {
		out.AdditionalProperties = &runtime.AdditionalPropertiesSpec{
			Allowed: s.AdditionalProperties.Allowed,
			Schema:  runtimeSchema(s.AdditionalProperties.Schema, defs, visited, request),
		}
	}
	if request {
		readOnly := map[string]bool{}
		collectReadOnlyProperties(s, defs, map[string]bool{}, readOnly)
		removeReadOnlyRequired(out, readOnly)
	}
	return out
}

func rawSchemaHasRefSiblings(s *rawir.RawSchema) bool {
	return s.Type != "" || s.Format != "" || len(s.Enum) > 0 || len(s.Properties) > 0 || len(s.Required) > 0 || s.Items != nil || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || len(s.AllOf) > 0 || s.AdditionalProperties != nil
}

func runtimeSchemas(schemas []*rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool, request bool) []*runtime.SchemaSpec {
	if len(schemas) == 0 {
		return nil
	}
	out := make([]*runtime.SchemaSpec, len(schemas))
	for i, schema := range schemas {
		out[i] = runtimeSchema(schema, defs, visited, request)
	}
	return out
}

func collectReadOnlyProperties(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool, names map[string]bool) {
	if s == nil {
		return
	}
	if s.Ref != "" && !visited[s.Ref] {
		visited[s.Ref] = true
		collectReadOnlyProperties(rawir.Resolve(s, defs), defs, visited, names)
	}
	for name, property := range s.Properties {
		if schemaReadOnly(property, defs, map[string]bool{}) {
			names[name] = true
		}
	}
	for _, child := range s.AllOf {
		collectReadOnlyProperties(child, defs, visited, names)
	}
}

func schemaReadOnly(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool) bool {
	if s == nil {
		return false
	}
	if s.ReadOnly {
		return true
	}
	if s.Ref != "" && !visited[s.Ref] {
		visited[s.Ref] = true
		if schemaReadOnly(rawir.Resolve(s, defs), defs, visited) {
			return true
		}
	}
	for _, child := range s.AllOf {
		if schemaReadOnly(child, defs, visited) {
			return true
		}
	}
	return false
}

func removeReadOnlyRequired(s *runtime.SchemaSpec, names map[string]bool) {
	if s == nil {
		return
	}
	kept := s.Required[:0]
	for _, name := range s.Required {
		if !names[name] {
			kept = append(kept, name)
		}
	}
	s.Required = kept
	for _, child := range s.AllOf {
		removeReadOnlyRequired(child, names)
	}
}
