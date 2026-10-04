package normalize

import (
	"fmt"
	"maps"
	"mime"
	"slices"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func parameter(p rawir.RawParameter) runtime.ParamSpec {
	if p.In == runtime.InFormData && p.Type == "file" && p.Format == "" {
		p.Format = "binary"
	}
	spec := runtime.ParamSpec{
		Name: p.Name, Flag: camelToKebab(p.Name), In: p.In, GoType: "string", Help: helpText(p),
		Required: p.Required, Default: p.Default, Enum: p.Enum, Format: p.Format, Deprecated: p.Deprecated,
	}
	switch p.In {
	case runtime.InPath:
		spec.Required = true
		if p.Type == "array" {
			spec.GoType = "[]string"
		}
	case runtime.InQuery, runtime.InFormData, runtime.InVariable:
		switch p.Type {
		case "integer":
			spec.GoType = "int64"
		case "boolean":
			spec.GoType = "bool"
		case "array":
			if p.In != runtime.InFormData {
				spec.GoType = "[]string"
			}
		}
		if p.In == runtime.InVariable {
			spec.Flag = variableFlagName(p.Name)
			if typ := map[string]string{"number": "float64", "integer-array": "[]int64", "number-array": "[]float64", "boolean-array": "[]bool", "string-array": "[]string"}[p.Type]; typ != "" {
				spec.GoType = typ
			}
		}
	}
	applySerialization(&spec, p)
	return spec
}

func applySerialization(spec *runtime.ParamSpec, p rawir.RawParameter) {
	switch p.In {
	case runtime.InPath, runtime.InQuery, runtime.InHeader, runtime.InCookie:
	default:
		return
	}
	spec.AllowReserved = p.AllowReserved && p.In == runtime.InQuery
	style, explode, err := serialization(p)
	if err != nil || defaultSerialization(p.In, style, explode) {
		return
	}
	spec.Style = style
	spec.Explode = explode
}

func variableFlagName(name string) string {
	return strings.ReplaceAll(camelToKebab(name), ".", "-")
}

func normalizeParamFlags(params []runtime.ParamSpec) {
	desired := make([]string, len(params))
	desiredCount := make(map[string]int, len(params))
	for i, param := range params {
		desired[i] = strings.Trim(collapseDashes(strings.ReplaceAll(param.Flag, "_", "-")), "-")
		desiredCount[desired[i]]++
	}
	for i := range params {
		if desired[i] == "" || desired[i] == params[i].Flag || desiredCount[desired[i]] > 1 {
			continue
		}
		params[i].Aliases = []string{params[i].Flag}
		params[i].Flag = desired[i]
	}
}

func multipartBodyParams(body *rawir.RawRequestBody, defs map[string]*rawir.RawSchema) ([]runtime.ParamSpec, []string) {
	mediaType, _, err := mime.ParseMediaType(body.MediaType)
	if err != nil || mediaType != "multipart/form-data" {
		return nil, nil
	}
	properties, requiredNames, ok := multipartObjectFields(body.Schema, defs, map[string]bool{})
	if !ok || len(properties) == 0 {
		return nil, nil
	}
	required := make(map[string]bool, len(requiredNames))
	for _, name := range requiredNames {
		required[name] = true
	}
	names := slices.Sorted(maps.Keys(properties))
	out := make([]runtime.ParamSpec, 0, len(names))
	var unsupported []string
	for _, name := range names {
		rawProperty := properties[name]
		if schemaReadOnly(rawProperty, defs, map[string]bool{}) {
			continue
		}
		property := multipartPropertySchema(rawProperty, defs)
		goType, format, defaultType, partOK := multipartPart(property, defs)
		if !partOK {
			unsupported = append(unsupported, name)
			continue
		}
		declaredFormat := ""
		propertyType := ""
		description := ""
		var enum []string
		if property != nil {
			declaredFormat = property.Format
			propertyType = property.Type
			description = property.Description
			enum = append([]string(nil), property.Enum...)
		}
		if format == "binary" {
			declaredFormat = "binary"
		}
		spec := parameter(rawir.RawParameter{
			Name:        name,
			In:          "formData",
			Required:    required[name],
			Description: description,
			Type:        propertyType,
			Format:      declaredFormat,
			Enum:        enum,
		})
		spec.GoType = goType
		if format != "" {
			spec.Format = format
		}
		if itemEnum := multipartItemEnum(property, defs); len(itemEnum) > 0 {
			spec.ItemEnum = itemEnum
		}
		declared := ""
		if body.PartContentTypes != nil {
			declared = body.PartContentTypes[name]
		}
		spec.ContentType = multipartContentType(declared, defaultType)
		out = append(out, spec)
	}
	return out, unsupported
}

func multipartObjectFields(schema *rawir.RawSchema, defs map[string]*rawir.RawSchema, seen map[string]bool) (map[string]*rawir.RawSchema, []string, bool) {
	if schema == nil {
		return nil, nil, false
	}
	if schema.Ref != "" {
		if seen[schema.Ref] {
			return nil, nil, false
		}
		seen[schema.Ref] = true
		defer delete(seen, schema.Ref)
		resolved := rawir.Resolve(schema, defs)
		if resolved == nil {
			return nil, nil, false
		}
		properties, required, ok := multipartObjectFields(resolved, defs, seen)
		if !ok {
			return nil, nil, false
		}
		if multipartSchemaHasKeywords(schema) || len(schema.AllOf) > 0 {
			local := *schema
			local.Ref = ""
			localProperties, localRequired, localOK := multipartObjectFields(&local, defs, seen)
			if !localOK {
				return nil, nil, false
			}
			properties, required = mergeMultipartFields(properties, required, localProperties, localRequired)
		}
		return properties, required, true
	}
	if len(schema.OneOf) > 0 || len(schema.AnyOf) > 0 {
		return nil, nil, false
	}
	if len(schema.AllOf) > 0 {
		if schema.Type != "" && schema.Type != "object" {
			return nil, nil, false
		}
		var properties map[string]*rawir.RawSchema
		var required []string
		if schema.Type == "object" || len(schema.Properties) > 0 || schema.AdditionalProperties != nil {
			properties, required = copyMultipartProperties(schema)
		}
		for _, child := range schema.AllOf {
			childProperties, childRequired, childOK := multipartObjectFields(child, defs, seen)
			if !childOK {
				return nil, nil, false
			}
			properties, required = mergeMultipartFields(properties, required, childProperties, childRequired)
		}
		return properties, required, true
	}
	if schema.Type == "object" || (schema.Type == "" && (len(schema.Properties) > 0 || schema.AdditionalProperties != nil)) {
		properties, required := copyMultipartProperties(schema)
		return properties, required, true
	}
	return nil, nil, false
}

func copyMultipartProperties(schema *rawir.RawSchema) (map[string]*rawir.RawSchema, []string) {
	properties := make(map[string]*rawir.RawSchema, len(schema.Properties))
	for name, property := range schema.Properties {
		properties[name] = property
	}
	return properties, append([]string(nil), schema.Required...)
}

func mergeMultipartFields(base map[string]*rawir.RawSchema, baseRequired []string, extra map[string]*rawir.RawSchema, extraRequired []string) (map[string]*rawir.RawSchema, []string) {
	if base == nil {
		base = map[string]*rawir.RawSchema{}
	}
	for name, property := range extra {
		if _, ok := base[name]; !ok {
			base[name] = property
		}
	}
	seen := make(map[string]bool, len(baseRequired)+len(extraRequired))
	required := make([]string, 0, len(baseRequired)+len(extraRequired))
	for _, name := range append(append([]string(nil), baseRequired...), extraRequired...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		required = append(required, name)
	}
	return base, required
}

func multipartPropertySchema(schema *rawir.RawSchema, defs map[string]*rawir.RawSchema) *rawir.RawSchema {
	return multipartUnwrap(schema, defs, map[string]bool{}, 0)
}

func multipartUnwrap(schema *rawir.RawSchema, defs map[string]*rawir.RawSchema, seen map[string]bool, depth int) *rawir.RawSchema {
	if schema == nil || depth > 16 {
		return schema
	}
	description := schema.Description
	if schema.Ref != "" && len(schema.AllOf) == 0 && !multipartSchemaHasKeywords(schema) {
		if seen[schema.Ref] {
			return schema
		}
		seen[schema.Ref] = true
		defer delete(seen, schema.Ref)
		resolved := rawir.Resolve(schema, defs)
		if resolved == nil {
			return schema
		}
		return overlayMultipartDescription(multipartUnwrap(resolved, defs, seen, depth+1), description)
	}
	if schema.Ref == "" && len(schema.AllOf) == 1 && !multipartSchemaHasKeywords(schema) {
		return overlayMultipartDescription(multipartUnwrap(schema.AllOf[0], defs, seen, depth+1), description)
	}
	return schema
}

func multipartSchemaHasKeywords(schema *rawir.RawSchema) bool {
	return schema.Type != "" || schema.Format != "" || len(schema.Enum) > 0 || len(schema.Properties) > 0 || schema.Items != nil || len(schema.AnyOf) > 0 || len(schema.OneOf) > 0 || schema.AdditionalProperties != nil
}

func overlayMultipartDescription(schema *rawir.RawSchema, description string) *rawir.RawSchema {
	if schema == nil || description == "" || schema.Description == description {
		return schema
	}
	clone := *schema
	clone.Description = description
	return &clone
}

func multipartPart(schema *rawir.RawSchema, defs map[string]*rawir.RawSchema) (goType, format, contentType string, ok bool) {
	if schema == nil {
		return "", "", "", false
	}
	if schema.Format == "binary" && (schema.Type == "" || schema.Type == "string") {
		return "string", "binary", "application/octet-stream", true
	}
	if multipartEmptySchema(schema) {
		return "string", "binary", "application/octet-stream", true
	}
	switch schema.Type {
	case "string", "number":
		return "string", "", "text/plain", true
	case "integer":
		return "int64", "", "text/plain", true
	case "boolean":
		return "bool", "", "text/plain", true
	case "object":
		return "string", "", "application/json", true
	case "array":
		return multipartArrayPart(schema, defs)
	}
	if schema.Type == "" && (len(schema.Properties) > 0 || schema.AdditionalProperties != nil) {
		return "string", "", "application/json", true
	}
	return "", "", "", false
}

func multipartArrayPart(schema *rawir.RawSchema, defs map[string]*rawir.RawSchema) (goType, format, contentType string, ok bool) {
	items := multipartPropertySchema(schema.Items, defs)
	if items == nil || multipartArrayItemsUnsupported(items) {
		return "", "", "", false
	}
	if items.Format == "binary" && (items.Type == "" || items.Type == "string") || multipartEmptySchema(items) {
		return "[]string", "binary", "application/octet-stream", true
	}
	switch items.Type {
	case "string", "number":
		return "[]string", "", "text/plain", true
	case "integer":
		return "[]int64", "", "text/plain", true
	case "boolean":
		return "[]bool", "", "text/plain", true
	default:
		return "", "", "", false
	}
}

func multipartArrayItemsUnsupported(items *rawir.RawSchema) bool {
	if items.Type == "array" || items.Type == "object" {
		return true
	}
	return items.Type == "" && (len(items.Properties) > 0 || items.AdditionalProperties != nil || len(items.AnyOf) > 0 || len(items.OneOf) > 0 || len(items.AllOf) > 0)
}

func multipartEmptySchema(schema *rawir.RawSchema) bool {
	return schema.Ref == "" && schema.Type == "" && schema.Format == "" && len(schema.Properties) == 0 && schema.Items == nil && len(schema.AnyOf) == 0 && len(schema.OneOf) == 0 && len(schema.AllOf) == 0 && schema.AdditionalProperties == nil && len(schema.Enum) == 0
}

func multipartItemEnum(schema *rawir.RawSchema, defs map[string]*rawir.RawSchema) []string {
	if schema == nil || schema.Type != "array" {
		return nil
	}
	items := multipartPropertySchema(schema.Items, defs)
	if items == nil || (items.Type != "string" && items.Type != "number") || len(items.Enum) == 0 {
		return nil
	}
	return append([]string(nil), items.Enum...)
}

func multipartContentType(declared, fallback string) string {
	if strings.TrimSpace(declared) == "" {
		return fallback
	}
	var kept []string
	for _, part := range strings.Split(declared, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		mediaType, params, err := mime.ParseMediaType(part)
		if err != nil || mediaType == "" {
			continue
		}
		formatted := mime.FormatMediaType(mediaType, params)
		if formatted == "" {
			continue
		}
		kept = append(kept, formatted)
	}
	if len(kept) == 0 {
		return fallback
	}
	return strings.Join(kept, ", ")
}

func disambiguateCookieParamFlags(params []runtime.ParamSpec) {
	used := make(map[string]bool, len(params))
	for _, param := range params {
		if param.In != runtime.InCookie {
			used[param.Flag] = true
		}
	}
	for i := range params {
		if params[i].In != runtime.InCookie {
			continue
		}
		flag := params[i].Flag
		if used[flag] {
			base := "cookie-" + flag
			flag = base
			for suffix := 2; used[flag]; suffix++ {
				flag = fmt.Sprintf("%s-%d", base, suffix)
			}
			params[i].Flag = flag
		}
		used[flag] = true
	}
}

func disambiguateMultipartParamFlags(existing, body []runtime.ParamSpec) {
	used := make(map[string]bool, len(existing)+len(body))
	for _, param := range existing {
		used[param.Flag] = true
	}
	for i := range body {
		flag := body[i].Flag
		if used[flag] {
			base := "body-" + flag
			flag = base
			for suffix := 2; used[flag]; suffix++ {
				flag = fmt.Sprintf("%s-%d", base, suffix)
			}
			body[i].Flag = flag
		}
		used[flag] = true
	}
}

func helpText(p rawir.RawParameter) string {
	base := strings.TrimSpace(p.Description)
	if base == "" {
		base = p.Name
	}
	base = firstLine(base)
	var parts []string
	parts = append(parts, p.In)
	if p.Required {
		parts = append(parts, "required")
	}
	if p.Format != "" {
		parts = append(parts, p.Format)
	}
	if p.In == "formData" && p.Format == "binary" {
		parts = append(parts, "local file path")
	}
	if len(p.Enum) > 0 {
		parts = append(parts, "one of: "+strings.Join(p.Enum, "|"))
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(parts, ", "))
}
