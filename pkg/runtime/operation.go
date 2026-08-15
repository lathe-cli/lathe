package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

type OperationInput struct {
	Values         map[string]any
	Changed        map[string]bool
	FileBody       []byte
	HasFile        bool
	BodySets       []string
	BodyStringSets []string
}

type OperationOptions struct {
	Hostname    string
	Client      ClientOptions
	DryRun      bool
	PaginateAll bool
	MaxPages    int
	Wait        bool
}

type OperationResult struct {
	Data    []byte
	DryRun  *DryRunRequest
	Outcome string
}

const (
	OperationOutcomeCompleted = "completed"
	OperationOutcomePaused    = "paused"
)

type DryRunRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
	Auth    DryRunAuth        `json:"auth"`
	Output  CatalogOutput     `json:"output"`
}

type DryRunAuth struct {
	Required bool     `json:"required"`
	Public   bool     `json:"public"`
	Scopes   []string `json:"scopes,omitempty"`
}

func InvokeOperation(ctx context.Context, s CommandSpec, input OperationInput, opts OperationOptions) (OperationResult, error) {
	return invokeOperation(ctx, s, input, opts, operationOutput{})
}

type operationOutput struct {
	raw  io.Writer
	live io.Writer
}

func invokeOperation(ctx context.Context, s CommandSpec, input OperationInput, opts OperationOptions, output operationOutput) (OperationResult, error) {
	path, body, clientOpts, err := resolveOperationRequest(s, input, opts.Client)
	if err != nil {
		return OperationResult{}, err
	}
	if opts.DryRun {
		out, err := buildDryRunRequest(ctx, s, opts.Hostname, path, body, clientOpts)
		if err != nil {
			return OperationResult{}, err
		}
		return OperationResult{DryRun: &out, Outcome: OperationOutcomeCompleted}, nil
	}

	var data []byte
	outcome := OperationOutcomeCompleted
	if opts.PaginateAll && s.Output.Pagination != nil {
		maxPages := opts.MaxPages
		if maxPages == 0 {
			maxPages = DefaultMaxPages
		}
		data, err = PaginateAll(ctx, opts.Hostname, s.Method, path, body, clientOpts, *s.Output.Pagination, s.Output.ListPath, maxPages)
	} else if opts.Wait {
		var r *RawResult
		r, err = DoRawFull(ctx, opts.Hostname, s.Method, path, body, clientOpts)
		if err == nil && r.StatusCode == 202 {
			if loc := r.Header.Get("Location"); loc != "" {
				data, err = PollUntilDone(ctx, opts.Hostname, loc, clientOpts, DefaultPollTimeout)
			} else {
				data = r.Body
			}
		} else if err == nil {
			data = r.Body
		}
	} else if output.raw != nil {
		_, err = doRawFull(ctx, opts.Hostname, s.Method, path, body, clientOpts, output.raw)
	} else if s.Output.Streaming != nil && s.Output.Streaming.Policy != nil && s.Output.Streaming.Policy.Collect != nil {
		var result *RawResult
		result, err = doRawFullConsume(ctx, opts.Hostname, s.Method, path, body, clientOpts, func(r io.Reader) ([]byte, error) {
			return collectStream(r, s.Output.Streaming, output.live, &outcome)
		})
		if err == nil {
			data = result.Body
		}
	} else {
		data, err = DoRaw(ctx, opts.Hostname, s.Method, path, body, clientOpts)
	}
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Data: data, Outcome: outcome}, nil
}

func resolveOperationRequest(s CommandSpec, input OperationInput, clientOpts ClientOptions) (string, any, ClientOptions, error) {
	if err := validateRequiredOperationParams(s, input); err != nil {
		return "", nil, ClientOptions{}, err
	}
	if err := validateOperationEnums(s, input); err != nil {
		return "", nil, ClientOptions{}, err
	}

	path := s.PathTpl
	q := url.Values{}
	hdrs := map[string]string{}
	form := url.Values{}
	files := map[string]string{}
	vars := map[string]any{}
	for _, p := range s.Params {
		switch p.In {
		case InPath:
			v, ok, err := operationValue(input, p)
			if err != nil {
				return "", nil, ClientOptions{}, err
			}
			if !ok {
				continue
			}
			path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(operationStringValue(v)), 1)
			continue
		case InHeader:
			if !operationChanged(input, p) {
				continue
			}
			v, _, err := operationValue(input, p)
			if err != nil {
				return "", nil, ClientOptions{}, err
			}
			hdrs[p.Name] = operationStringValue(v)
			continue
		case InVariable:
			if !operationChanged(input, p) {
				continue
			}
			v, _, err := operationValue(input, p)
			if err != nil {
				return "", nil, ClientOptions{}, err
			}
			vars[p.Name] = v
			continue
		case InFormData:
			if !operationChanged(input, p) {
				continue
			}
			v, _, err := operationValue(input, p)
			if err != nil {
				return "", nil, ClientOptions{}, err
			}
			if p.Format == "binary" {
				files[p.Name] = operationStringValue(v)
			} else {
				form.Set(p.Name, operationStringValue(v))
			}
			continue
		}
		if !operationChanged(input, p) {
			continue
		}
		if p.In == InQuery && isSensitiveStringParam(p) {
			if clientOpts.sensitiveQueryParams == nil {
				clientOpts.sensitiveQueryParams = map[string]bool{}
			}
			clientOpts.sensitiveQueryParams[strings.ToLower(p.Name)] = true
		}
		v, _, err := operationValue(input, p)
		if err != nil {
			return "", nil, ClientOptions{}, err
		}
		switch tv := v.(type) {
		case int64:
			q.Set(p.Name, strconv.FormatInt(tv, 10))
		case bool:
			q.Set(p.Name, strconv.FormatBool(tv))
		case []string:
			for _, vv := range tv {
				q.Add(p.Name, vv)
			}
		case string:
			q.Set(p.Name, tv)
		}
	}
	if enc := q.Encode(); enc != "" {
		path = path + "?" + enc
	}

	body, err := resolveOperationBody(s, input, form, files, vars)
	if err != nil {
		return "", nil, ClientOptions{}, err
	}
	if err := validateRequiredVariableParams(s, body); err != nil {
		return "", nil, ClientOptions{}, err
	}
	if body != nil && s.RequestBody != nil && s.RequestBody.MediaType != "" && !isMultipartMediaType(s.RequestBody.MediaType) {
		hdrs["Content-Type"] = s.RequestBody.MediaType
	}

	clientOpts.Headers = hdrs
	if s.Output.ResponseMediaType != "" {
		clientOpts.Accept = s.Output.ResponseMediaType
	}
	return path, body, clientOpts, nil
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
	switch {
	case len(input.BodySets) > 0 || len(input.BodyStringSets) > 0:
		if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
			return nil, NewLatheError(CodeUsage, ExitUsage, fmt.Errorf("request body media type %s requires --file; --set and --set-str only support JSON request bodies", s.RequestBody.MediaType))
		}
		body, err := buildBodyFromSet(input.BodySets, input.BodyStringSets)
		if err != nil {
			return nil, NewLatheError(CodeUsage, ExitUsage, err)
		}
		return body, nil
	case input.HasFile:
		return input.FileBody, nil
	case s.RequestBody.Required:
		if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
			return nil, NewLatheError(CodeUsage, ExitUsage, fmt.Errorf("request body media type %s requires --file", s.RequestBody.MediaType))
		}
		return nil, NewLatheError(CodeUsage, ExitUsage, fmt.Errorf("request body required: pass --file, --set, or --set-str"))
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

func buildDryRunRequest(ctx context.Context, s CommandSpec, hostname, path string, body any, opts ClientOptions) (DryRunRequest, error) {
	req, bodyBytes, _, err := resolveRequest(ctx, hostname, s.Method, path, body, opts)
	if err != nil {
		return DryRunRequest{}, err
	}
	return DryRunRequest{
		Method:  req.Method,
		URL:     redactDebugURL(req.URL, opts.sensitiveQueryParams),
		Headers: redactedDryRunHeaders(req.Header),
		Body:    redactedDryRunBody(req.Header.Get("Content-Type"), bodyBytes),
		Auth:    dryRunAuthForSpec(s),
		Output: CatalogOutput{
			ListPath:          s.Output.ListPath,
			DefaultColumns:    append([]string(nil), s.Output.DefaultColumns...),
			ResponseMediaType: s.Output.ResponseMediaType,
			Pagination:        catalogPagination(s.Output.Pagination),
			Streaming:         catalogStreaming(s.Output.Streaming),
		},
	}, nil
}

func validateRequiredOperationParams(s CommandSpec, input OperationInput) error {
	for _, p := range s.Params {
		if !p.Required || p.Default != "" {
			continue
		}
		if p.In == InVariable && s.RequestBody != nil {
			continue
		}
		if !operationChanged(input, p) {
			return NewLatheError(CodeUsage, ExitUsage, fmt.Errorf("required param %q missing", p.Name))
		}
	}
	return nil
}

func validateOperationEnums(s CommandSpec, input OperationInput) error {
	for _, p := range s.Params {
		if len(p.Enum) == 0 || !operationChanged(input, p) {
			continue
		}
		v, _, err := operationValue(input, p)
		if err != nil {
			return NewLatheError(CodeUsage, ExitUsage, err)
		}
		raw := operationStringValue(v)
		valid := false
		for _, e := range p.Enum {
			if raw == e {
				valid = true
				break
			}
		}
		if !valid {
			return NewLatheError(CodeUsage, ExitUsage, fmt.Errorf("invalid value %q for --%s: must be one of %s", raw, p.Flag, strings.Join(p.Enum, ", ")))
		}
	}
	return nil
}

func operationChanged(input OperationInput, p ParamSpec) bool {
	if p.Default != "" {
		return true
	}
	if input.Changed != nil {
		return input.Changed[boundParamKey(p)] || input.Changed[p.Name] || input.Changed[p.Flag]
	}
	if _, ok := input.Values[boundParamKey(p)]; ok {
		return true
	}
	if _, ok := input.Values[p.Name]; ok {
		return true
	}
	if _, ok := input.Values[p.Flag]; ok {
		return true
	}
	return false
}

func operationValue(input OperationInput, p ParamSpec) (any, bool, error) {
	v, ok := input.Values[boundParamKey(p)]
	if !ok {
		v, ok = input.Values[p.Name]
	}
	if !ok {
		v, ok = input.Values[p.Flag]
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
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, err
		}
		return v, nil
	case "float64":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, err
		}
		return v, nil
	case "bool":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, err
		}
		return v, nil
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

func writeDryRun(out DryRunRequest, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
