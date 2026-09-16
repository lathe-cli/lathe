package runtime

import (
	"context"
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
	q := url.Values{}
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
				path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(operationStringValue(v)), 1)
			}
		case InHeader:
			hdrs[p.Name] = operationStringValue(v)
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
