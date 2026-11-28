package runtime

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func requiredFlagParam(p ParamSpec, hasRequestBody bool) bool {
	return p.Required && p.Default == "" && p.In != InBody && (p.In != InVariable || !hasRequestBody)
}

func validateRequiredOperationParams(s CommandSpec, input OperationInput) error {
	for _, p := range s.Params {
		if !requiredFlagParam(p, s.RequestBody != nil) {
			continue
		}
		if !operationChanged(input, p) {
			return WithUsageDetail(fmt.Errorf("required param %q missing", p.Name), "missing required: --"+p.Flag)
		}
	}
	return nil
}

func validateOperationEnums(s CommandSpec, input OperationInput) error {
	for _, p := range s.Params {
		if len(p.Enum) == 0 && len(p.ItemEnum) == 0 || !operationChanged(input, p) {
			continue
		}
		v, _, err := operationValue(input, p)
		if err != nil {
			return err
		}
		if len(p.Enum) > 0 {
			raw := operationStringValue(v)
			if !slices.Contains(p.Enum, raw) {
				return WithUsageDetail(
					fmt.Errorf("invalid value %q for --%s: must be one of %s", raw, p.Flag, strings.Join(p.Enum, ", ")),
					enumDetail(p.Flag, p.Enum),
				)
			}
		}
		for _, raw := range operationStringValues(v) {
			if len(p.ItemEnum) > 0 && !slices.Contains(p.ItemEnum, raw) {
				return WithUsageDetail(
					fmt.Errorf("invalid item %q for --%s: must be one of %s", raw, p.Flag, strings.Join(p.ItemEnum, ", ")),
					enumDetail(p.Flag, p.ItemEnum),
				)
			}
		}
	}
	return nil
}

func enumDetail(flag string, values []string) string {
	return fmt.Sprintf("--%s accepts: %s", flag, strings.Join(values, ", "))
}

func operationStringValues(v any) []string {
	switch tv := v.(type) {
	case []string:
		return append([]string(nil), tv...)
	case []int64:
		out := make([]string, len(tv))
		for i, value := range tv {
			out[i] = strconv.FormatInt(value, 10)
		}
		return out
	case []float64:
		out := make([]string, len(tv))
		for i, value := range tv {
			out[i] = strconv.FormatFloat(value, 'f', -1, 64)
		}
		return out
	case []bool:
		out := make([]string, len(tv))
		for i, value := range tv {
			out[i] = strconv.FormatBool(value)
		}
		return out
	default:
		return nil
	}
}

func operationChanged(input OperationInput, p ParamSpec) bool {
	return p.Default != "" || operationValueProvided(p, input)
}

func operationValue(input OperationInput, p ParamSpec) (any, bool, error) {
	v, ok := input.Values[p.Key()]
	if !ok {
		v, ok = input.Values[boundParamKey(p)]
	}
	if !ok {
		if p.Default == "" {
			return nil, false, nil
		}
		v = p.Default
	}
	out, err := coerceOperationValue(v, p)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func boundParamKey(p ParamSpec) string {
	return p.In + "\x00" + p.Flag
}

func coerceOperationValue(v any, p ParamSpec) (any, error) {
	if p.In == InPath || p.In == InQuery || p.In == InHeader || p.In == InCookie || p.In == InFormData {
		array := false
		switch v.(type) {
		case string, *string, json.Number, int, int64, *int64, float64, *float64, bool, *bool:
		case []string, *[]string, []int64, *[]int64, []float64, *[]float64, []bool, *[]bool:
			array = true
		default:
			return nil, fmt.Errorf("unsupported value for %s parameter %q", p.In, p.Name)
		}
		if array && !strings.HasPrefix(p.GoType, "[]") {
			return nil, fmt.Errorf("%s parameter %q requires a scalar value", p.In, p.Name)
		}
	}

	switch tv := v.(type) {
	case *string:
		return *tv, nil
	case *int64:
		return *tv, nil
	case *float64:
		return *tv, nil
	case *bool:
		return *tv, nil
	case *[]int64:
		return append([]int64(nil), (*tv)...), nil
	case *[]float64:
		return append([]float64(nil), (*tv)...), nil
	case *[]bool:
		return append([]bool(nil), (*tv)...), nil
	case *[]string:
		return append([]string(nil), (*tv)...), nil
	case string:
		return parseStringOperationValue(tv, p)
	case json.Number:
		return parseStringOperationValue(tv.String(), p)
	case int:
		return int64(tv), nil
	case int64:
		return tv, nil
	case float64:
		return tv, nil
	case bool:
		return tv, nil
	case []int64:
		return append([]int64(nil), tv...), nil
	case []float64:
		return append([]float64(nil), tv...), nil
	case []bool:
		return append([]bool(nil), tv...), nil
	case []string:
		return append([]string(nil), tv...), nil
	default:
		return tv, nil
	}
}

func parseStringOperationValue(raw string, p ParamSpec) (any, error) {
	switch p.GoType {
	case "int64":
		return strconv.ParseInt(raw, 10, 64)
	case "float64":
		return strconv.ParseFloat(raw, 64)
	case "bool":
		return strconv.ParseBool(raw)
	default:
		return raw, nil
	}
}

func operationStringValue(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	case int64:
		return strconv.FormatInt(tv, 10)
	case float64:
		return strconv.FormatFloat(tv, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(tv)
	case []int64:
		if len(tv) > 0 {
			return strconv.FormatInt(tv[0], 10)
		}
	case []float64:
		if len(tv) > 0 {
			return strconv.FormatFloat(tv[0], 'f', -1, 64)
		}
	case []bool:
		if len(tv) > 0 {
			return strconv.FormatBool(tv[0])
		}
	case []string:
		if len(tv) > 0 {
			return tv[0]
		}
	}
	return ""
}

func operationValueProvided(param ParamSpec, input OperationInput) bool {
	if input.Changed != nil {
		return input.Changed[param.Key()] || input.Changed[boundParamKey(param)]
	}
	for _, key := range []string{param.Key(), boundParamKey(param)} {
		if _, ok := input.Values[key]; ok {
			return true
		}
	}
	return false
}

func ParamKey(in, name string) string {
	return in + ":" + name
}

func (p ParamSpec) Key() string {
	return ParamKey(p.In, p.Name)
}

func canonicalOperationInput(params []ParamSpec, input OperationInput) (OperationInput, error) {
	values := make(map[string]any, len(input.Values))
	changed := make(map[string]bool, len(input.Changed))
	for key, value := range input.Values {
		index, ok := runtimeParamIndex(params, key)
		if !ok {
			if paramLookupMatches(params, key) {
				return OperationInput{}, ambiguousParamError(key)
			}
			values[key] = value
			continue
		}
		canonical := params[index].Key()
		if _, exists := values[canonical]; exists {
			return OperationInput{}, WithUsageDetail(fmt.Errorf("parameter %q is supplied more than once", canonical), "use one parameter key per value")
		}
		values[canonical] = value
	}
	for key, value := range input.Changed {
		index, ok := runtimeParamIndex(params, key)
		if !ok {
			if value && paramLookupMatches(params, key) {
				return OperationInput{}, ambiguousParamError(key)
			}
			changed[key] = value
			continue
		}
		changed[params[index].Key()] = changed[params[index].Key()] || value
	}
	input.Values = values
	if input.Changed != nil {
		input.Changed = changed
	}
	return input, nil
}

func paramLookupMatches(params []ParamSpec, key string) bool {
	for _, p := range params {
		if key == p.Name || key == p.Flag || key == p.Key() || key == boundParamKey(p) {
			return true
		}
	}
	return false
}

func ambiguousParamError(key string) error {
	return WithUsageDetail(fmt.Errorf("parameter %q is ambiguous", key), "use in:name or a unique flag name")
}
