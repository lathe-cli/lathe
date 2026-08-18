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
	if value == nil && schema.Nullable {
		return nil
	}
	if err := validateSchemaCompositions(schema, value, path); err != nil {
		return err
	}
	if value == nil {
		if expected == "" || expected == "null" {
			return nil
		}
		return schemaTypeError(path, expected, value)
	}
	if expected == "" {
		switch value := value.(type) {
		case map[string]any:
			return validateSchemaObject(schema, value, path)
		case []any:
			return validateSchemaArray(schema, value, path)
		default:
			return nil
		}
	}

	switch expected {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return schemaTypeError(path, expected, value)
		}
		return validateSchemaObject(schema, object, path)
	case "array":
		array, ok := value.([]any)
		if !ok {
			return schemaTypeError(path, expected, value)
		}
		return validateSchemaArray(schema, array, path)
	case "string":
		if _, ok := value.(string); ok {
			return nil
		}
		if number, ok := value.(json.Number); schema.AcceptIntegerEnum && ok && isJSONInteger(number) {
			return nil
		}
		return schemaTypeError(path, expected, value)
	case "boolean":
		if _, ok := value.(bool); !ok {
			return schemaTypeError(path, expected, value)
		}
	case "number":
		if _, ok := value.(json.Number); ok {
			return nil
		}
		if text, ok := value.(string); schema.AcceptStringEncodedNumber && ok && isStringEncodedJSONNumber(text) {
			return nil
		}
		return schemaTypeError(path, expected, value)
	case "integer":
		if number, ok := value.(json.Number); ok && isJSONInteger(number) {
			return nil
		}
		if text, ok := value.(string); schema.AcceptStringEncodedInteger && ok && isStringEncodedJSONInteger(text) {
			return nil
		}
		return schemaTypeError(path, expected, value)
	case "null":
		return schemaTypeError(path, expected, value)
	default:
		return nil
	}
	return nil
}

func validateSchemaCompositions(schema *SchemaSpec, value any, path string) error {
	for _, branch := range schema.AllOf {
		if err := validateSchemaValue(branch, value, path); err != nil {
			return err
		}
	}
	if len(schema.AnyOf) > 0 {
		matched := false
		for _, branch := range schema.AnyOf {
			if validateSchemaValue(branch, value, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("request body %s: does not match any schema in anyOf", path)
		}
	}
	if len(schema.OneOf) > 0 {
		matches := 0
		for _, branch := range schema.OneOf {
			if validateSchemaValue(branch, value, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("request body %s: matches %d schemas in oneOf, want exactly one", path, matches)
		}
	}
	return nil
}

func validateSchemaObject(schema *SchemaSpec, object map[string]any, path string) error {
	for _, name := range schema.Required {
		if _, ok := object[name]; !ok {
			return fmt.Errorf("request body %s: required field missing", schemaPropertyPath(path, name))
		}
	}
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child := object[name]
		property, declared := schema.Properties[name]
		if declared {
			if err := validateSchemaValue(property, child, schemaPropertyPath(path, name)); err != nil {
				return err
			}
			continue
		}
		additional := schema.AdditionalProperties
		if additional == nil {
			continue
		}
		if additional.Schema != nil {
			if err := validateSchemaValue(additional.Schema, child, schemaPropertyPath(path, name)); err != nil {
				return err
			}
			continue
		}
		if additional.Allowed {
			continue
		}
		return fmt.Errorf("request body %s: additional field not allowed", schemaPropertyPath(path, name))
	}
	return nil
}

func validateSchemaArray(schema *SchemaSpec, array []any, path string) error {
	for i, item := range array {
		if err := validateSchemaValue(schema.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
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

func isStringEncodedJSONInteger(value string) bool {
	return json.Valid([]byte(value)) && isJSONInteger(json.Number(value))
}

func isStringEncodedJSONNumber(value string) bool {
	if value == "NaN" || value == "Infinity" || value == "-Infinity" {
		return true
	}
	_, ok := new(big.Float).SetString(value)
	return ok && json.Valid([]byte(value))
}

func schemaPropertyPath(parent, name string) string {
	if name != "" && !strings.ContainsAny(name, ".[]") {
		return parent + "." + name
	}
	return parent + "[" + strconv.Quote(name) + "]"
}
