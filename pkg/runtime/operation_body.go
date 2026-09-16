package runtime

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func supportsJSONBodyBuilder(mediaType string) bool {
	mt, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mediaType)), ";")
	mt = strings.TrimSpace(mt)
	return mt == "" || mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func resolveOperationBody(s CommandSpec, input OperationInput, form url.Values, files map[string]string, vars map[string]any) (any, error) {
	if len(files) > 0 || (isMultipartMediaType(requestBodyMediaType(s)) && (len(form) > 0 || s.RequestBody.Required && hasFormDataParams(s.Params))) {
		return multipartForm{Fields: form, Files: files}, nil
	}
	if len(form) > 0 {
		return form, nil
	}
	if s.RequestBody == nil {
		return nil, nil
	}
	if s.RequestBody.Template != "" {
		return buildEnvelopeBody(s.RequestBody.Template, s.RequestBody.MergePath, vars, input.BodySets, input.BodyStringSets, input.FileBody, input.HasFile)
	}
	hasSets := len(input.BodySets) > 0 || len(input.BodyStringSets) > 0
	flagBody, flagFields, err := jsonBodyFromFlags(s, input)
	if err != nil {
		return nil, err
	}
	if hasJSONBodyFlags(s.Params) && input.HasFile && (hasSets || len(flagFields) > 0) {
		return nil, fmt.Errorf("--file cannot be combined with --set, --set-str, or body flags")
	}
	if len(flagFields) > 0 || (hasJSONBodyFlags(s.Params) && hasSets) {
		if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
			return nil, fmt.Errorf("request body media type %s requires --file; --set and --set-str only support JSON request bodies", s.RequestBody.MediaType)
		}
		if err := mergeJSONBodySets(flagBody, flagFields, input.BodySets, input.BodyStringSets); err != nil {
			return nil, err
		}
		return json.Marshal(flagBody)
	}
	switch {
	case hasSets:
		if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
			return nil, fmt.Errorf("request body media type %s requires --file; --set and --set-str only support JSON request bodies", s.RequestBody.MediaType)
		}
		return buildBodyFromSet(input.BodySets, input.BodyStringSets)
	case input.HasFile:
		return input.FileBody, nil
	case s.RequestBody.Required:
		if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
			return nil, fmt.Errorf("request body media type %s requires --file", s.RequestBody.MediaType)
		}
		if hasJSONBodyFlags(s.Params) {
			return nil, WithUsageDetail(fmt.Errorf("request body required"), "request body required: pass --file, --set, --set-str, or a body flag")
		}
		return nil, WithUsageDetail(fmt.Errorf("request body required"), "request body required: pass --file, --set, or --set-str")
	default:
		return nil, nil
	}
}

func requestBodyMediaType(s CommandSpec) string {
	if s.RequestBody == nil {
		return ""
	}
	return s.RequestBody.MediaType
}

func hasFormDataParams(params []ParamSpec) bool {
	for _, param := range params {
		if param.In == InFormData {
			return true
		}
	}
	return false
}

func validateRequiredVariableParams(s CommandSpec, body any) error {
	if s.RequestBody == nil {
		return nil
	}
	required := make([]ParamSpec, 0)
	for _, p := range s.Params {
		if p.In == InVariable && p.Required && p.Default == "" {
			required = append(required, p)
		}
	}
	if len(required) == 0 {
		return nil
	}
	raw, ok := body.([]byte)
	if !ok || len(raw) == 0 {
		return missingBodyFieldError(required[0].Name)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("validate request body: %w", err)
	}
	for _, p := range required {
		v, ok := getNestedPath(doc, joinBodyPath(s.RequestBody.MergePath, p.Name))
		if !ok || v == nil {
			return missingBodyFieldError(p.Name)
		}
	}
	return nil
}

func validateRequiredBodyParams(s CommandSpec, body any) error {
	if body == nil {
		return nil
	}
	required := make([]ParamSpec, 0)
	for _, p := range s.Params {
		if p.In == InBody && p.Required && p.Default == "" {
			required = append(required, p)
		}
	}
	setOnlyRequired := requiredSetOnlyFields(s)
	if len(required) == 0 && len(setOnlyRequired) == 0 {
		return nil
	}
	raw, _, err := encodeRequestBody(body)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("validate request body: %w", err)
	}
	for _, p := range required {
		if _, ok := doc[p.Name]; !ok {
			return missingBodyFieldError(p.Name)
		}
	}
	for _, name := range setOnlyRequired {
		if _, ok := doc[name]; !ok {
			return missingBodyFieldError(name)
		}
	}
	return nil
}

func requiredSetOnlyFields(s CommandSpec) []string {
	if s.RequestBody == nil || len(s.RequestBody.SetOnlyFields) == 0 || s.RequestBody.Schema == nil {
		return nil
	}
	setOnly := make(map[string]bool, len(s.RequestBody.SetOnlyFields))
	for _, name := range s.RequestBody.SetOnlyFields {
		setOnly[name] = true
	}
	out := make([]string, 0)
	for _, name := range s.RequestBody.Schema.Required {
		if setOnly[name] {
			out = append(out, name)
		}
	}
	return out
}

func missingBodyFieldError(name string) error {
	return WithUsageDetail(fmt.Errorf("required body field missing: %s", name), "missing required: "+name)
}
