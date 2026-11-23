package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
			if _, ok := ve.ErrorKind.(*kind.AnyOf); ok && len(ve.Causes) == 2 {
				first, second := ve.Causes[0], ve.Causes[1]
				typ, typeOK := first.ErrorKind.(*kind.Type)
				null, nullOK := second.ErrorKind.(*kind.Type)
				if typeOK && nullOK && slices.Equal(first.InstanceLocation, ve.InstanceLocation) && slices.Equal(second.InstanceLocation, ve.InstanceLocation) && slices.Equal(null.Want, []string{"null"}) {
					return staticBodyTypeError(s, first.InstanceLocation, append(slices.Clone(typ.Want), "null"))
				}
			}
			ve = ve.Causes[0]
		}
		if required, ok := ve.ErrorKind.(*kind.Required); ok && len(required.Missing) > 0 {
			missing := slices.Clone(required.Missing)
			slices.Sort(missing)
			for i, name := range missing {
				missing[i] = staticBodyPointer(s, append(slices.Clone(ve.InstanceLocation), name))
			}
			return staticBodySchemaError("request body missing required: " + strings.Join(missing, ", "))
		}
		if typ, ok := ve.ErrorKind.(*kind.Type); ok {
			return staticBodyTypeError(s, ve.InstanceLocation, typ.Want)
		}
		return staticBodySchemaError(fmt.Sprintf("request body at %s does not satisfy %s", staticBodyPointer(s, ve.InstanceLocation), strings.Join(ve.ErrorKind.KeywordPath(), "/")))
	}
	return nil
}

func staticBodyPointer(s CommandSpec, path []string) string {
	path = slices.Clone(path)
	if s.RequestBody.Template != "" && s.RequestBody.MergePath != "" {
		path = append(strings.Split(s.RequestBody.MergePath, "."), path...)
	}
	for i, part := range path {
		path[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	return "#/" + strings.Join(path, "/")
}

func staticBodyTypeError(s CommandSpec, path, want []string) error {
	detail := fmt.Sprintf("request body at %s must be %s", staticBodyPointer(s, path), strings.Join(want, " or "))
	field := ""
	for _, part := range path {
		if _, err := strconv.Atoi(part); err == nil {
			field += "[" + part + "]"
		} else {
			if field != "" {
				field += "."
			}
			field += part
		}
	}
	switch {
	case !slices.Contains(want, "string") && slices.Contains(want, "array"):
		if field == "" || strings.HasPrefix(field, "[") {
			return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; use --%[1]s for a JSON array", detail))
		}
		return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; use --%[2]s %[5]s[0]=... or --%[2]s %[5]s=[] or --%[1]s; JSON arrays passed to --%[2]s remain strings", detail, field))
	case !slices.Contains(want, "string") && slices.Contains(want, "object"):
		if strings.HasPrefix(field, "[") {
			return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; use --%[1]s for a JSON object in the array", detail))
		}
		if field != "" {
			field += "."
		}
		return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; use --%[2]s %[5]s<field>=... or --%[1]s; JSON objects passed to --%[2]s remain strings", detail, field))
	case slices.Equal(want, []string{"string"}) || slices.Equal(want, []string{"string", "null"}):
		return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; use --%[3]s to keep numbers, booleans, null, or [] as string literals", detail))
	case !slices.Contains(want, "string") && (slices.Contains(want, "integer") || slices.Contains(want, "number") || slices.Contains(want, "boolean")):
		return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; use --%[2]s for numbers, booleans, or null instead of --%[3]s, or pass JSON with --%[1]s", detail))
	default:
		return staticBodySchemaUsageError(bodyUsageDetail("%[4]s; pass JSON with --%[1]s", detail))
	}
}

func staticBodySchemaDocument(doc map[string]any) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	nullable, _ := doc["nullable"].(bool)
	format, _ := doc["format"].(string)
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
		if typ == "string" && (format == "int64" || format == "uint64") {
			doc["type"] = []any{"string", "integer"}
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
	return staticBodySchemaUsageError(WithUsageDetail(errors.New(detail), detail))
}

func staticBodySchemaUsageError(cause error) error {
	err := NewError(CodeUsage, ExitUsage, "request body does not match the compiled schema", "inspect the command body schema and correct the request body", &staticBodySchemaFailure{cause: cause})
	var detail *usageDetailError
	if errors.As(cause, &detail) {
		err.Detail = detail.detail
	}
	return err
}

type staticBodySchemaFailure struct {
	cause error
}

func (e *staticBodySchemaFailure) Error() string {
	return e.cause.Error()
}

func (e *staticBodySchemaFailure) Unwrap() error {
	return e.cause
}
