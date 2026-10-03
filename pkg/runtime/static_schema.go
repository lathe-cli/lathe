package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

func validateStaticBodySchema(s CommandSpec, body any) error {
	if body == nil || s.RequestBody == nil || s.RequestBody.Schema == nil || !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
		return nil
	}
	instance, err := requestBodyJSONValue(body)
	if err != nil {
		return staticBodySchemaError("request body must contain one valid JSON value")
	}
	if s.RequestBody.Template != "" && s.RequestBody.MergePath != "" {
		instance, _ = getNestedPath(instance, s.RequestBody.MergePath)
	}
	raw, err := json.Marshal(s.RequestBody.Schema)
	if err != nil {
		return staticBodySchemaError("invalid compiled request body schema")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return staticBodySchemaError("invalid compiled request body schema")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(nil)
	const resource = "urn:lathe:static-body-schema"
	if err := compiler.AddResource(resource, staticBodySchemaDocument(doc)); err != nil {
		return staticBodySchemaError("invalid compiled request body schema")
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return staticBodySchemaError("invalid compiled request body schema")
	}
	if err := schema.Validate(instance); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			return staticBodySchemaError("request body does not match the compiled schema")
		}
		for len(ve.Causes) > 0 {
			ve = ve.Causes[0]
		}
		path := append([]string(nil), ve.InstanceLocation...)
		if s.RequestBody.Template != "" && s.RequestBody.MergePath != "" {
			path = append(strings.Split(s.RequestBody.MergePath, "."), path...)
		}
		if required, ok := ve.ErrorKind.(*kind.Required); ok && len(required.Missing) > 0 {
			path = append(path, required.Missing[0])
		}
		for i, part := range path {
			path[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
		}
		return staticBodySchemaError(fmt.Sprintf("request body at #/%s does not satisfy %s", strings.Join(path, "/"), strings.Join(ve.ErrorKind.KeywordPath(), "/")))
	}
	return nil
}

func staticBodySchemaDocument(doc map[string]any) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	nullable, _ := doc["nullable"].(bool)
	for key := range doc {
		switch key {
		case "type", "properties", "required", "items", "allOf":
		default:
			delete(doc, key)
		}
	}
	if typ, ok := doc["type"].(string); ok {
		switch typ {
		case "object", "array", "string", "integer", "number", "boolean", "null":
		default:
			delete(doc, "type")
		}
	}
	if required, ok := doc["required"].([]any); ok {
		seen := map[string]bool{}
		unique := make([]any, 0, len(required))
		for _, item := range required {
			if name, ok := item.(string); ok && !seen[name] {
				unique = append(unique, name)
				seen[name] = true
			}
		}
		doc["required"] = unique
	}
	if properties, ok := doc["properties"].(map[string]any); ok {
		for name, property := range properties {
			child, _ := property.(map[string]any)
			properties[name] = staticBodySchemaDocument(child)
		}
	}
	if items, ok := doc["items"].(map[string]any); ok {
		doc["items"] = staticBodySchemaDocument(items)
	}
	if allOf, ok := doc["allOf"].([]any); ok {
		for i, item := range allOf {
			child, _ := item.(map[string]any)
			allOf[i] = staticBodySchemaDocument(child)
		}
	}
	if nullable {
		return map[string]any{"anyOf": []any{doc, map[string]any{"type": "null"}}}
	}
	return doc
}

func staticBodySchemaError(detail string) error {
	err := NewError(CodeUsage, ExitUsage, "request body does not match the compiled schema", "inspect the command body schema and correct the request body", WithUsageDetail(errors.New(detail), detail))
	err.Detail = sanitizeErrorDetail(detail)
	return err
}
