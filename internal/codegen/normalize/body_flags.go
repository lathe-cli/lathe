package normalize

import (
	"fmt"
	"mime"
	"sort"
	"strings"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

var reservedBodyFlagNames = map[string]bool{
	"all":       true,
	"debug":     true,
	"dry-run":   true,
	"file":      true,
	"help":      true,
	"hostname":  true,
	"max-pages": true,
	"output":    true,
	"set":       true,
	"set-str":   true,
	"stream":    true,
	"version":   true,
	"wait":      true,
}

func ExpandJSONBodyFlags(spec runtime.CommandSpec) ([]runtime.ParamSpec, error) {
	if spec.RequestBody == nil {
		return nil, fmt.Errorf("command has no request body")
	}
	if spec.RequestBody.Template != "" {
		return nil, fmt.Errorf("GraphQL request templates cannot enable body flags")
	}
	mediaType, _, err := mime.ParseMediaType(spec.RequestBody.MediaType)
	if spec.RequestBody.MediaType != "" && err != nil {
		return nil, fmt.Errorf("request body media type: %w", err)
	}
	if mediaType == "" {
		mediaType = spec.RequestBody.MediaType
	}
	if isMultipartMediaType(mediaType) || hasFormDataParams(spec.Params) {
		return nil, fmt.Errorf("multipart request bodies cannot enable body flags")
	}
	if !runtimeJSONBodyMediaType(mediaType) {
		return nil, fmt.Errorf("request body media type %q cannot enable body flags", spec.RequestBody.MediaType)
	}
	schema := spec.RequestBody.Schema
	if schema == nil {
		return nil, fmt.Errorf("request body schema is required")
	}
	if err := rejectUnsupportedJSONBodySchema(schema, true); err != nil {
		return nil, err
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	existing := map[string]bool{}
	for _, param := range spec.Params {
		existing[param.Flag] = true
		existing[param.Name] = true
		for _, alias := range param.Aliases {
			existing[alias] = true
		}
	}
	out := make([]runtime.ParamSpec, 0, len(names))
	seenFlags := map[string]string{}
	for _, name := range names {
		property := schema.Properties[name]
		goType, err := jsonBodyFlagGoType(property)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", name, err)
		}
		flag := camelToKebab(name)
		if flag == "" {
			return nil, fmt.Errorf("property %q does not produce a flag name", name)
		}
		if reservedBodyFlagNames[flag] {
			return nil, fmt.Errorf("property %q flag %q conflicts with a reserved flag", name, flag)
		}
		if existing[flag] || existing[name] {
			return nil, fmt.Errorf("property %q flag %q conflicts with an existing parameter", name, flag)
		}
		if prior, ok := seenFlags[flag]; ok {
			return nil, fmt.Errorf("properties %q and %q produce the same flag %q", prior, name, flag)
		}
		seenFlags[flag] = name
		out = append(out, runtime.ParamSpec{
			Name:     name,
			Flag:     flag,
			In:       runtime.InBody,
			GoType:   goType,
			Help:     jsonBodyFlagHelp(name, property, required[name]),
			Required: required[name],
			Enum:     append([]string(nil), property.Enum...),
			ItemEnum: jsonBodyItemEnum(property),
			Format:   property.Format,
		})
	}
	return out, nil
}

func ValidateJSONBodyFlagParams(spec runtime.CommandSpec) error {
	hasBodyFlags := false
	for _, param := range spec.Params {
		if param.In == runtime.InBody {
			hasBodyFlags = true
			break
		}
	}
	if !hasBodyFlags {
		return nil
	}
	if err := runtime.ValidateParamFlags(spec.Params); err != nil {
		return err
	}
	for _, param := range spec.Params {
		names := append([]string{param.Flag}, param.Aliases...)
		for _, name := range names {
			if name == "" {
				continue
			}
			if param.In == runtime.InBody && reservedBodyFlagNames[name] {
				return fmt.Errorf("body property %q flag %q conflicts with a reserved flag", param.Name, name)
			}
		}
	}
	return nil
}

func jsonBodyFlagGoType(schema *runtime.SchemaSpec) (string, error) {
	if schema == nil {
		return "", fmt.Errorf("schema is required")
	}
	if err := rejectUnsupportedJSONBodySchema(schema, false); err != nil {
		return "", err
	}
	switch schema.Type {
	case "string":
		return "string", nil
	case "integer":
		return "int64", nil
	case "number":
		return "float64", nil
	case "boolean":
		return "bool", nil
	case "array":
		if schema.Items == nil {
			return "", fmt.Errorf("array items are required")
		}
		itemType, err := jsonBodyFlagGoType(schema.Items)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(itemType, "[]") {
			return "", fmt.Errorf("nested arrays are not supported")
		}
		return "[]" + itemType, nil
	default:
		return "", fmt.Errorf("unsupported type %q", schema.Type)
	}
}

func rejectUnsupportedJSONBodySchema(schema *runtime.SchemaSpec, root bool) error {
	if schema == nil {
		return fmt.Errorf("schema is required")
	}
	if schema.Ref != "" && schema.Type == "" && len(schema.Properties) == 0 && schema.Items == nil {
		return fmt.Errorf("unresolved schema refs are not supported")
	}
	if len(schema.AnyOf) > 0 || len(schema.OneOf) > 0 || len(schema.AllOf) > 0 {
		return fmt.Errorf("oneOf/anyOf/allOf is not supported")
	}
	if schema.AdditionalProperties != nil && (schema.AdditionalProperties.Allowed || schema.AdditionalProperties.Schema != nil) {
		return fmt.Errorf("maps are not supported")
	}
	if root {
		if schema.Type != "object" {
			return fmt.Errorf("request body must be a JSON object")
		}
		if len(schema.Properties) == 0 {
			return fmt.Errorf("request body object has no properties")
		}
		return nil
	}
	if schema.Type == "object" || len(schema.Properties) > 0 {
		return fmt.Errorf("nested objects are not supported")
	}
	return nil
}

func jsonBodyFlagHelp(name string, schema *runtime.SchemaSpec, required bool) string {
	base := name
	if schema != nil && strings.TrimSpace(schema.Description) != "" {
		base = firstLine(schema.Description)
	}
	parts := []string{"body"}
	if required {
		parts = append(parts, "required")
	}
	if schema != nil && schema.Format != "" {
		parts = append(parts, schema.Format)
	}
	if schema != nil && len(schema.Enum) > 0 {
		parts = append(parts, "one of: "+strings.Join(schema.Enum, "|"))
	}
	if items := jsonBodyItemEnum(schema); len(items) > 0 {
		parts = append(parts, "items one of: "+strings.Join(items, "|"))
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(parts, ", "))
}

func jsonBodyItemEnum(schema *runtime.SchemaSpec) []string {
	if schema == nil || schema.Type != "array" || schema.Items == nil {
		return nil
	}
	return append([]string(nil), schema.Items.Enum...)
}

func runtimeJSONBodyMediaType(mediaType string) bool {
	mt, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mediaType)), ";")
	mt = strings.TrimSpace(mt)
	return mt == "" || mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func isMultipartMediaType(mediaType string) bool {
	mt, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mediaType)), ";")
	return strings.TrimSpace(mt) == "multipart/form-data"
}

func hasFormDataParams(params []runtime.ParamSpec) bool {
	for _, param := range params {
		if param.In == runtime.InFormData {
			return true
		}
	}
	return false
}
