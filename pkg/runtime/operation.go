package runtime

import (
	"cmp"
	"context"
	"io"
	"net/url"
	"slices"
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
	HostSource  string
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
		out, err := buildDryRunRequest(ctx, s, opts.Hostname, opts.HostSource, path, body, clientOpts)
		if err != nil {
			return OperationResult{}, err
		}
		return OperationResult{DryRun: &out, Outcome: OperationOutcomeCompleted}, nil
	}
	if !securitySatisfied(s.Security, clientOpts.Auth, path, clientOpts.Headers) {
		return OperationResult{}, unsatisfiedSecurityError(opts.Hostname, s.Security)
	}
	if s.RequestBody != nil && s.RequestBody.RuntimeSchema != nil {
		if err := validateRuntimeSchemaBody(ctx, s, input, body, opts); err != nil {
			return OperationResult{}, err
		}
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
		if s.Output.Binary {
			clientOpts.binaryContentGuard = true
		}
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
	if outcome == OperationOutcomeCompleted {
		if err := persistOperationContext(ctx, s, input, opts.Hostname); err != nil {
			return OperationResult{}, err
		}
	}
	return OperationResult{Data: data, Outcome: outcome}, nil
}

func validateOperationInput(s CommandSpec, input OperationInput) error {
	_, body, _, err := resolveOperationRequest(s, input, ClientOptions{})
	if err != nil {
		return err
	}
	_, _, err = encodeRequestBody(body)
	return err
}

func resolveOperationRequest(s CommandSpec, input OperationInput, clientOpts ClientOptions) (string, any, ClientOptions, error) {
	if err := validateRequiredOperationParams(s, input); err != nil {
		return "", nil, ClientOptions{}, err
	}
	if err := validateOperationEnums(s, input); err != nil {
		return "", nil, ClientOptions{}, err
	}

	path := s.PathTpl
	var pairs []queryPair
	var cookies []string
	hdrs := map[string]string{}
	form := url.Values{}
	files := map[string]string{}
	vars := map[string]any{}
	for _, p := range s.Params {
		if p.In == InBody || (p.In != InPath && !operationChanged(input, p)) {
			continue
		}
		v, present, err := operationValue(input, p)
		if err != nil {
			return "", nil, ClientOptions{}, err
		}
		switch p.In {
		case InPath:
			if present {
				path = strings.Replace(path, "{"+p.Name+"}", pathParamValue(p, v), 1)
			}
		case InHeader:
			hdrs[p.Name] = operationStringValue(v)
		case InCookie:
			cookies = append(cookies, p.Name+"="+cookieEscape(operationStringValue(v)))
		case InVariable:
			vars[p.Name] = v
		case InFormData:
			if p.Format == "binary" {
				files[p.Name] = operationStringValue(v)
			} else {
				form.Set(p.Name, operationStringValue(v))
			}
		default:
			if p.In == InQuery && isSensitiveStringParam(p) {
				if clientOpts.sensitiveQueryParams == nil {
					clientOpts.sensitiveQueryParams = map[string]bool{}
				}
				clientOpts.sensitiveQueryParams[strings.ToLower(p.Name)] = true
			}
			pairs = append(pairs, queryParamPairs(p, v)...)
		}
	}
	if len(cookies) > 0 {
		hdrs["Cookie"] = strings.Join(cookies, "; ")
	}
	if len(pairs) > 0 {
		path = path + "?" + encodeQueryPairs(pairs)
	}

	body, err := resolveOperationBody(s, input, form, files, vars)
	if err != nil {
		return "", nil, ClientOptions{}, err
	}
	if err := validateStaticBodySchema(s, body); err != nil {
		return "", nil, ClientOptions{}, err
	}
	if err := validateRequiredVariableParams(s, body); err != nil {
		return "", nil, ClientOptions{}, err
	}
	if err := validateRequiredBodyParams(s, body); err != nil {
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

type queryPair struct {
	key string
	raw string
}

var queryDelimiters = map[string]string{
	"form":           ",",
	"spaceDelimited": "%20",
	"pipeDelimited":  "%7C",
}

var reservedQueryReplacer = strings.NewReplacer(
	"%3A", ":",
	"%2F", "/",
	"%3F", "?",
	"%5B", "[",
	"%5D", "]",
	"%40", "@",
	"%21", "!",
	"%24", "$",
	"%27", "'",
	"%28", "(",
	"%29", ")",
	"%2A", "*",
	"%2C", ",",
	"%3B", ";",
)

func pathParamValue(p ParamSpec, v any) string {
	items := pathParamItems(v)
	escaped := make([]string, len(items))
	for i, item := range items {
		escaped[i] = url.PathEscape(item)
	}
	switch p.Style {
	case "label":
		sep := ","
		if p.Explode {
			sep = "."
		}
		return "." + strings.Join(escaped, sep)
	case "matrix":
		prefix := ";" + url.PathEscape(p.Name) + "="
		if p.Explode {
			return prefix + strings.Join(escaped, prefix)
		}
		return prefix + strings.Join(escaped, ",")
	default:
		return strings.Join(escaped, ",")
	}
}

func pathParamItems(v any) []string {
	switch tv := v.(type) {
	case []string:
		return tv
	default:
		return []string{operationStringValue(v)}
	}
}

func cookieEscape(v string) string {
	return strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
}

func reservedQueryEscape(v string) string {
	return reservedQueryReplacer.Replace(url.QueryEscape(v))
}

func queryEscape(p ParamSpec, value string) string {
	if p.AllowReserved {
		return reservedQueryEscape(value)
	}
	return url.QueryEscape(value)
}

func queryParamPairs(p ParamSpec, v any) []queryPair {
	switch tv := v.(type) {
	case []string:
		return queryArrayPairs(p, tv)
	case int64:
		return []queryPair{{key: p.Name, raw: queryEscape(p, strconv.FormatInt(tv, 10))}}
	case bool:
		return []queryPair{{key: p.Name, raw: queryEscape(p, strconv.FormatBool(tv))}}
	case string:
		return []queryPair{{key: p.Name, raw: queryEscape(p, tv)}}
	default:
		return nil
	}
}

func queryArrayPairs(p ParamSpec, values []string) []queryPair {
	if len(values) == 0 {
		return nil
	}
	if p.Style == "" || p.Explode {
		out := make([]queryPair, len(values))
		for i, value := range values {
			out[i] = queryPair{key: p.Name, raw: queryEscape(p, value)}
		}
		return out
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = queryEscape(p, value)
	}
	return []queryPair{{key: p.Name, raw: strings.Join(parts, queryDelimiters[p.Style])}}
}

func encodeQueryPairs(pairs []queryPair) string {
	slices.SortStableFunc(pairs, func(a, b queryPair) int {
		return cmp.Compare(a.key, b.key)
	})
	var b strings.Builder
	for i, pair := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(pair.key))
		b.WriteByte('=')
		b.WriteString(pair.raw)
	}
	return b.String()
}
