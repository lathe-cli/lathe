package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

func validateStaticRequestBody(spec CommandSpec, body any) error {
	requestBody := spec.RequestBody
	if requestBody == nil || requestBody.Schema == nil || requestBody.Template != "" || body == nil || !supportsJSONBodyBuilder(requestBody.MediaType) {
		return nil
	}
	raw, _, err := encodeRequestBody(body)
	if err != nil {
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("decode request body JSON: invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode request body JSON: %w", err)
	}
	return validateSchemaValue(requestBody.Schema, value, "$")
}

func validateSchemaValue(schema *SchemaSpec, value any, path string) error {
	if schema == nil {
		return nil
	}
	expected := schema.Type
	if expected == "" {
		switch {
		case len(schema.Properties) > 0 || len(schema.Required) > 0:
			expected = "object"
		case schema.Items != nil:
			expected = "array"
		}
	}
	if value == nil {
		if schema.Nullable || expected == "" || expected == "null" {
			return nil
		}
		return schemaTypeError(path, expected, value)
	}

	switch expected {
	case "":
		return nil
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return schemaTypeError(path, expected, value)
		}
		for _, name := range schema.Required {
			if _, ok := object[name]; !ok {
				return fmt.Errorf("request body %s: required field missing", schemaPropertyPath(path, name))
			}
		}
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, ok := object[name]
			if !ok {
				continue
			}
			if err := validateSchemaValue(schema.Properties[name], child, schemaPropertyPath(path, name)); err != nil {
				return err
			}
		}
		return nil
	case "array":
		array, ok := value.([]any)
		if !ok {
			return schemaTypeError(path, expected, value)
		}
		for i, item := range array {
			if err := validateSchemaValue(schema.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case "string":
		if _, ok := value.(string); !ok {
			return schemaTypeError(path, expected, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return schemaTypeError(path, expected, value)
		}
	case "number":
		if _, ok := value.(json.Number); !ok {
			return schemaTypeError(path, expected, value)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok || !isJSONInteger(number) {
			return schemaTypeError(path, expected, value)
		}
	case "null":
		return schemaTypeError(path, expected, value)
	default:
		return nil
	}
	return nil
}

func schemaTypeError(path, expected string, value any) error {
	return fmt.Errorf("request body %s: expected %s, got %s", path, expected, jsonValueType(value))
}

func jsonValueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	default:
		return "unknown"
	}
}

func isJSONInteger(number json.Number) bool {
	value, ok := new(big.Rat).SetString(number.String())
	return ok && value.IsInt()
}

func schemaPropertyPath(parent, name string) string {
	if name != "" && !strings.ContainsAny(name, ".[]") {
		return parent + "." + name
	}
	return parent + "[" + strconv.Quote(name) + "]"
}
