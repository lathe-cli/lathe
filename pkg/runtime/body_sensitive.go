package runtime

import (
	"fmt"
	"strings"
)

func validateSensitiveBodySets(spec CommandSpec, sets, stringSets []string) error {
	for i, assignments := range [][]string{sets, stringSets} {
		flag := "--set"
		if i == 1 {
			flag = "--set-str"
		}
		for _, assignment := range assignments {
			path, _, err := parseSet(assignment, flag)
			if err != nil {
				return err
			}
			segments, err := parsePath(path)
			if err != nil {
				return err
			}
			sensitive := false
			hint := "use --file or --file -"
			for _, param := range spec.Params {
				if (param.In != InBody && param.In != InVariable) || !isSensitiveStringParam(param) {
					continue
				}
				if path == param.Name || strings.HasPrefix(path, param.Name+".") || strings.HasPrefix(path, param.Name+"[") {
					sensitive = true
					hint += fmt.Sprintf("; typed input also accepts --%s-env, --%s-file, or --%s-stdin", param.Flag, param.Flag, param.Flag)
				}
			}
			if spec.RequestBody != nil {
				sensitive = sensitive || sensitiveSchemaPath(spec.RequestBody.Schema, segments)
				if spec.RequestBody.MergePath != "" {
					merged, err := parsePath(joinBodyPath(spec.RequestBody.MergePath, path))
					if err != nil {
						return err
					}
					sensitive = sensitive || sensitiveSchemaPath(spec.RequestBody.Schema, merged)
				}
			}
			if sensitive {
				return NewError(CodeUsage, ExitUsage, fmt.Sprintf("sensitive body field %q rejects --set and --set-str", path), hint, nil)
			}
		}
	}
	return nil
}

func sensitiveSchemaPath(schema *SchemaSpec, path []pathSegment) bool {
	if schema == nil {
		return false
	}
	if strings.EqualFold(schema.Format, "password") {
		return true
	}
	for _, alternatives := range [][]*SchemaSpec{schema.AnyOf, schema.OneOf, schema.AllOf} {
		for _, branch := range alternatives {
			if sensitiveSchemaPath(branch, path) {
				return true
			}
		}
	}
	if len(path) == 0 {
		return false
	}
	segment := path[0]
	property := schema.Properties[segment.key]
	if property == nil && schema.AdditionalProperties != nil {
		property = schema.AdditionalProperties.Schema
	}
	if property == nil {
		return false
	}
	if isSensitiveStringParam(ParamSpec{Name: segment.key, GoType: "string", Format: property.Format}) {
		return true
	}
	if segment.idx >= 0 {
		return sensitiveSchemaItems(property, path[1:])
	}
	return sensitiveSchemaPath(property, path[1:])
}

func sensitiveSchemaItems(schema *SchemaSpec, path []pathSegment) bool {
	if schema == nil {
		return false
	}
	if strings.EqualFold(schema.Format, "password") {
		return true
	}
	for _, alternatives := range [][]*SchemaSpec{schema.AnyOf, schema.OneOf, schema.AllOf} {
		for _, branch := range alternatives {
			if sensitiveSchemaItems(branch, path) {
				return true
			}
		}
	}
	return sensitiveSchemaPath(schema.Items, path)
}
