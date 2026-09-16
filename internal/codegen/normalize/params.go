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
	return spec
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

func multipartBodyParams(body *rawir.RawRequestBody, defs map[string]*rawir.RawSchema) []runtime.ParamSpec {
	mediaType, _, err := mime.ParseMediaType(body.MediaType)
	if err != nil || mediaType != "multipart/form-data" {
		return nil
	}
	schema := rawir.Resolve(body.Schema, defs)
	if schema == nil || schema.Type != "object" || len(schema.Properties) == 0 {
		return nil
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	names := slices.Sorted(maps.Keys(schema.Properties))
	out := make([]runtime.ParamSpec, 0, len(names))
	for _, name := range names {
		property := rawir.Resolve(schema.Properties[name], defs)
		if property == nil || !multipartScalar(property) {
			return nil
		}
		out = append(out, parameter(rawir.RawParameter{
			Name:     name,
			In:       "formData",
			Required: required[name],
			Type:     property.Type,
			Format:   property.Format,
		}))
	}
	return out
}

func multipartScalar(schema *rawir.RawSchema) bool {
	if schema.Format == "binary" {
		return schema.Type == "" || schema.Type == "string"
	}
	switch schema.Type {
	case "string", "integer", "boolean":
		return true
	default:
		return false
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
