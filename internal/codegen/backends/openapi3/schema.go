package openapi3

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/backends/document"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"gopkg.in/yaml.v3"
)

type schemaNode struct {
	Ref                  string                      `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Type                 schemaType                  `json:"type,omitempty" yaml:"type,omitempty"`
	Description          string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Format               string                      `json:"format,omitempty" yaml:"format,omitempty"`
	Default              any                         `json:"default,omitempty" yaml:"default,omitempty"`
	Enum                 []any                       `json:"enum,omitempty" yaml:"enum,omitempty"`
	Nullable             bool                        `json:"nullable,omitempty" yaml:"nullable,omitempty"`
	Properties           map[string]*schemaNode      `json:"properties,omitempty" yaml:"properties,omitempty"`
	Required             []string                    `json:"required,omitempty" yaml:"required,omitempty"`
	Items                *schemaNode                 `json:"items,omitempty" yaml:"items,omitempty"`
	AnyOf                []*schemaNode               `json:"anyOf,omitempty" yaml:"anyOf,omitempty"`
	OneOf                []*schemaNode               `json:"oneOf,omitempty" yaml:"oneOf,omitempty"`
	AllOf                []*schemaNode               `json:"allOf,omitempty" yaml:"allOf,omitempty"`
	AdditionalProperties *schemaAdditionalProperties `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
}

type schemaType struct {
	Value    string
	Nullable bool
}

type schemaAdditionalProperties struct {
	Allowed bool
	Schema  *schemaNode
}

func (t *schemaType) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		t.Value = single
		t.Nullable = false
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("schema type must be string or string array: %w", err)
	}
	primary, err := primarySchemaType(many)
	if err != nil {
		return err
	}
	t.Value = primary
	t.Nullable = primary != "null" && slices.Contains(many, "null")
	return nil
}

func (t *schemaType) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		*t = schemaType{}
		return nil
	}

	var single string
	if err := value.Decode(&single); err == nil {
		t.Value = single
		t.Nullable = false
		return nil
	}

	var many []string
	if err := value.Decode(&many); err != nil {
		return fmt.Errorf("schema type must be string or string array: %w", err)
	}
	primary, err := primarySchemaType(many)
	if err != nil {
		return err
	}
	t.Value = primary
	t.Nullable = primary != "null" && slices.Contains(many, "null")
	return nil
}

func (p *schemaAdditionalProperties) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("true")) || bytes.Equal(data, []byte("false")) {
		p.Schema = nil
		return json.Unmarshal(data, &p.Allowed)
	}
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("additionalProperties must be a boolean or schema")
	}
	var schema schemaNode
	if err := json.Unmarshal(data, &schema); err != nil {
		return err
	}
	p.Allowed = false
	p.Schema = &schema
	return nil
}

func (p *schemaAdditionalProperties) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		return fmt.Errorf("additionalProperties must be a boolean or schema")
	}
	var allowed bool
	if err := value.Decode(&allowed); err == nil {
		p.Allowed = allowed
		p.Schema = nil
		return nil
	}
	var schema schemaNode
	if err := value.Decode(&schema); err != nil {
		return fmt.Errorf("additionalProperties must be a boolean or schema: %w", err)
	}
	p.Allowed = false
	p.Schema = &schema
	return nil
}

func primarySchemaType(types []string) (string, error) {
	if len(types) == 0 {
		return "", fmt.Errorf("schema type array must not be empty")
	}
	primary := ""
	for _, typ := range types {
		if typ == "null" {
			continue
		}
		if primary != "" {
			return "", fmt.Errorf("unsupported schema type union %q", types)
		}
		primary = typ
	}
	if primary == "" {
		return "null", nil
	}
	return primary, nil
}

const oas3RefPrefix = "#/components/schemas/"

func convertSchema(s *schemaNode) *rawir.RawSchema {
	if s == nil {
		return nil
	}
	if len(s.OneOf) == 0 && len(s.AllOf) == 0 {
		if option, ok := nullableAlternative(s, s.AnyOf); ok {
			out := convertSchema(option)
			if s.Description != "" {
				out.Description = s.Description
			}
			out.Nullable = true
			return out
		}
	}
	out := &rawir.RawSchema{
		Type:        s.Type.Value,
		Description: s.Description,
		Format:      s.Format,
		Nullable:    s.Nullable || s.Type.Nullable,
		Enum:        document.Strings(s.Enum),
	}
	if s.Ref != "" {
		if strings.HasPrefix(s.Ref, oas3RefPrefix) {
			out.Ref = rawir.RefPrefix + s.Ref[len(oas3RefPrefix):]
		} else {
			out.Ref = s.Ref
		}
	}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*rawir.RawSchema, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = convertSchema(v)
		}
	}
	if len(s.Required) > 0 {
		out.Required = append([]string(nil), s.Required...)
	}
	if s.Items != nil {
		out.Items = convertSchema(s.Items)
	}
	out.AnyOf = convertSchemas(s.AnyOf)
	out.OneOf = convertSchemas(s.OneOf)
	out.AllOf = convertSchemas(s.AllOf)
	if s.AdditionalProperties != nil {
		out.AdditionalProperties = &rawir.RawAdditionalProperties{
			Allowed: s.AdditionalProperties.Allowed,
			Schema:  convertSchema(s.AdditionalProperties.Schema),
		}
	}
	return out
}

func nullableAlternative(schema *schemaNode, options []*schemaNode) (*schemaNode, bool) {
	if len(options) != 2 || schema.Ref != "" || schema.Type.Value != "" || schema.Format != "" || len(schema.Properties) > 0 || len(schema.Required) > 0 || schema.Items != nil || schema.AdditionalProperties != nil {
		return nil, false
	}
	if isNullSchema(options[0]) {
		return options[1], options[1] != nil && !isNullSchema(options[1])
	}
	if isNullSchema(options[1]) {
		return options[0], options[0] != nil && !isNullSchema(options[0])
	}
	return nil, false
}

func isNullSchema(schema *schemaNode) bool {
	return schema != nil && schema.Type.Value == "null"
}

func convertSchemas(schemas []*schemaNode) []*rawir.RawSchema {
	if len(schemas) == 0 {
		return nil
	}
	out := make([]*rawir.RawSchema, len(schemas))
	for i, schema := range schemas {
		out[i] = convertSchema(schema)
	}
	return out
}
