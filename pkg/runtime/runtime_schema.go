package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strings"
	"unicode/utf8"
)

var runtimeSchemaAnnotations = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true,
	"default": true, "examples": true, "readOnly": true, "writeOnly": true,
	"deprecated": true,
}

func validateRuntimeRequestBody(ctx context.Context, target CommandSpec, input OperationInput, body any, opts OperationOptions) error {
	source := target.RequestBody.RuntimeSchema
	if source.Operation.Method != "GET" {
		return newAPIError(fmt.Errorf("runtime schema operation must use GET"), 0)
	}
	if source.Operation.Output.Streaming != nil || source.Operation.RequestBody != nil && source.Operation.RequestBody.RuntimeSchema != nil {
		return newAPIError(fmt.Errorf("runtime schema operation must be non-streaming and non-recursive"), 0)
	}
	sourceInput, err := runtimeSchemaOperationInput(target, input, source)
	if err != nil {
		return newAPIError(err, 0)
	}
	result, err := InvokeOperation(ctx, source.Operation, sourceInput, OperationOptions{Hostname: opts.Hostname, Client: opts.Client})
	if err != nil {
		return err
	}

	response, err := decodeJSON(result.Data)
	if err != nil {
		return newAPIError(fmt.Errorf("decode runtime schema response: %w", err), 0)
	}
	schema, ok := getNestedPath(response, source.ResponsePath)
	if !ok {
		return newAPIError(fmt.Errorf("runtime schema response path %q not found", source.ResponsePath), 0)
	}
	value, err := requestBodyJSONValue(body)
	if err != nil {
		return NewError(CodeUsage, ExitUsage, "request body does not match runtime schema", "inspect the declared schema operation and correct the request body", err)
	}
	if err := validateRuntimeSchemaDefinition(schema, "$schema"); err != nil {
		return newAPIError(err, 0)
	}
	if err := validateRuntimeJSONSchema(schema, value, "$", "$schema"); err != nil {
		return NewError(CodeUsage, ExitUsage, "request body does not match runtime schema", "inspect the declared schema operation and correct the request body", err)
	}
	return nil
}

func runtimeSchemaOperationInput(target CommandSpec, input OperationInput, source *RuntimeSchemaSource) (OperationInput, error) {
	out := OperationInput{Values: map[string]any{}, Changed: map[string]bool{}}
	for key, expression := range source.Params {
		param, ok := findOperationParam(source.Operation.Params, key)
		if !ok {
			return OperationInput{}, fmt.Errorf("runtime schema parameter %q does not exist", key)
		}
		value := any(expression)
		if ref, ok := runtimeSchemaParamRef(expression); ok {
			targetParam, found := findOperationParam(target.Params, ref)
			if !found {
				return OperationInput{}, fmt.Errorf("runtime schema target parameter %q does not exist", ref)
			}
			resolved, changed, err := operationValue(input, targetParam)
			if err != nil {
				return OperationInput{}, err
			}
			if !changed {
				if !param.Required || param.Default != "" {
					continue
				}
				return OperationInput{}, fmt.Errorf("runtime schema target parameter %q is not set", ref)
			}
			value = resolved
		}
		out.Values[param.Name] = value
		out.Changed[param.Name] = true
	}
	return out, nil
}

func runtimeSchemaParamRef(value string) (string, bool) {
	const prefix = "${params."
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "}") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, prefix), "}")
	return name, name != "" && !strings.ContainsAny(name, "${}")
}

func findOperationParam(params []ParamSpec, name string) (ParamSpec, bool) {
	for _, param := range params {
		if param.Name == name || param.Flag == name {
			return param, true
		}
	}
	return ParamSpec{}, false
}

func requestBodyJSONValue(body any) (any, error) {
	if body == nil {
		return nil, nil
	}
	if data, ok := body.([]byte); ok {
		return decodeJSON(data)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return decodeJSON(data)
}

func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON value")
	}
	return value, nil
}

func validateRuntimeSchemaDefinition(schema any, path string) error {
	if _, ok := schema.(bool); ok {
		return nil
	}
	object, ok := schema.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be an object or boolean", path)
	}
	for keyword := range object {
		if runtimeSchemaAnnotations[keyword] {
			continue
		}
		switch keyword {
		case "type", "properties", "required", "additionalProperties", "items", "enum", "minLength", "maxLength":
		default:
			return fmt.Errorf("%s uses unsupported keyword %q", path, keyword)
		}
	}
	if rawType, exists := object["type"]; exists {
		typeName, ok := rawType.(string)
		if !ok || !runtimeJSONTypeSupported(typeName) {
			return fmt.Errorf("%s.type is unsupported", path)
		}
	}
	properties, err := schemaObject(object["properties"], path+".properties")
	if err != nil {
		return err
	}
	for name, child := range properties {
		if err := validateRuntimeSchemaDefinition(child, path+".properties."+name); err != nil {
			return err
		}
	}
	if _, err := schemaStringArray(object["required"], path+".required"); err != nil {
		return err
	}
	if additional, exists := object["additionalProperties"]; exists {
		if _, ok := additional.(bool); !ok {
			if err := validateRuntimeSchemaDefinition(additional, path+".additionalProperties"); err != nil {
				return err
			}
		}
	}
	if items, exists := object["items"]; exists {
		if err := validateRuntimeSchemaDefinition(items, path+".items"); err != nil {
			return err
		}
	}
	if enum, exists := object["enum"]; exists {
		values, ok := enum.([]any)
		if !ok || len(values) == 0 {
			return fmt.Errorf("%s.enum must be a non-empty array", path)
		}
	}
	minimum, hasMinimum := object["minLength"]
	if hasMinimum {
		if _, err := schemaNonNegativeInt(minimum, path+".minLength"); err != nil {
			return err
		}
	}
	maximum, hasMaximum := object["maxLength"]
	if hasMaximum {
		if _, err := schemaNonNegativeInt(maximum, path+".maxLength"); err != nil {
			return err
		}
	}
	if hasMinimum && hasMaximum {
		min, _ := schemaNonNegativeInt(minimum, path+".minLength")
		max, _ := schemaNonNegativeInt(maximum, path+".maxLength")
		if min > max {
			return fmt.Errorf("%s.minLength exceeds maxLength", path)
		}
	}
	return nil
}

func validateRuntimeJSONSchema(schema, value any, valuePath, schemaPath string) error {
	if allowed, ok := schema.(bool); ok {
		if !allowed {
			return fmt.Errorf("%s is not allowed", valuePath)
		}
		return nil
	}
	object, ok := schema.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be an object or boolean", schemaPath)
	}
	for keyword := range object {
		if runtimeSchemaAnnotations[keyword] {
			continue
		}
		switch keyword {
		case "type", "properties", "required", "additionalProperties", "items", "enum", "minLength", "maxLength":
		default:
			return fmt.Errorf("%s uses unsupported keyword %q", schemaPath, keyword)
		}
	}
	if rawType, exists := object["type"]; exists {
		typeName, ok := rawType.(string)
		if !ok {
			return fmt.Errorf("%s.type must be a string", schemaPath)
		}
		if !runtimeJSONTypeMatches(typeName, value) {
			return fmt.Errorf("%s must be %s", valuePath, typeName)
		}
	}
	if enum, exists := object["enum"]; exists {
		values, ok := enum.([]any)
		if !ok {
			return fmt.Errorf("%s.enum must be an array", schemaPath)
		}
		matched := false
		for _, candidate := range values {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not an allowed value", valuePath)
		}
	}
	if text, ok := value.(string); ok {
		length := utf8.RuneCountInString(text)
		if raw, exists := object["minLength"]; exists {
			minimum, err := schemaNonNegativeInt(raw, schemaPath+".minLength")
			if err != nil {
				return err
			}
			if length < minimum {
				return fmt.Errorf("%s must contain at least %d characters", valuePath, minimum)
			}
		}
		if raw, exists := object["maxLength"]; exists {
			maximum, err := schemaNonNegativeInt(raw, schemaPath+".maxLength")
			if err != nil {
				return err
			}
			if length > maximum {
				return fmt.Errorf("%s must contain at most %d characters", valuePath, maximum)
			}
		}
	}
	if objectValue, ok := value.(map[string]any); ok {
		properties, err := schemaObject(object["properties"], schemaPath+".properties")
		if err != nil {
			return err
		}
		required, err := schemaStringArray(object["required"], schemaPath+".required")
		if err != nil {
			return err
		}
		for _, name := range required {
			if _, exists := objectValue[name]; !exists {
				return fmt.Errorf("%s.%s is required", valuePath, name)
			}
		}
		for name, child := range objectValue {
			if childSchema, exists := properties[name]; exists {
				if err := validateRuntimeJSONSchema(childSchema, child, valuePath+"."+name, schemaPath+".properties."+name); err != nil {
					return err
				}
				continue
			}
			additional, exists := object["additionalProperties"]
			if !exists || additional == true {
				continue
			}
			if additional == false {
				return fmt.Errorf("%s.%s is not allowed", valuePath, name)
			}
			if err := validateRuntimeJSONSchema(additional, child, valuePath+"."+name, schemaPath+".additionalProperties"); err != nil {
				return err
			}
		}
	}
	if items, exists := object["items"]; exists {
		array, ok := value.([]any)
		if !ok {
			return nil
		}
		for index, child := range array {
			if err := validateRuntimeJSONSchema(items, child, fmt.Sprintf("%s[%d]", valuePath, index), schemaPath+".items"); err != nil {
				return err
			}
		}
	}
	return nil
}

func runtimeJSONTypeMatches(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		rat, ok := new(big.Rat).SetString(number.String())
		return ok && rat.IsInt()
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func runtimeJSONTypeSupported(typeName string) bool {
	switch typeName {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func schemaObject(value any, path string) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	return object, nil
}

func schemaStringArray(value any, path string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	out := make([]string, 0, len(array))
	for _, item := range array {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must contain only strings", path)
		}
		out = append(out, text)
	}
	return out, nil
}

func schemaNonNegativeInt(value any, path string) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be a non-negative integer", path)
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", path)
	}
	return int(parsed), nil
}
