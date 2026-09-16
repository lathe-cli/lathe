package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func redactedDryRunHeaders(headers map[string][]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, vs := range headers {
		out[k] = redactDebugHeader(k, strings.Join(vs, ", "), nil)
	}
	return out
}

func redactedDryRunBody(contentType string, body []byte, sensitive map[string]bool) any {
	if len(body) == 0 {
		return nil
	}
	if isMultipartMediaType(contentType) {
		return fmt.Sprintf("<multipart body omitted: %d bytes>", len(body))
	}
	redacted := redactDebugBody(contentType, body, sensitive)
	if strings.HasPrefix(contentType, "application/json") {
		var v any
		if err := json.Unmarshal(redacted, &v); err == nil {
			return v
		}
	}
	return string(redacted)
}

func dryRunAuthForSpec(s CommandSpec) DryRunAuth {
	auth := catalogAuth(s.Security)
	return DryRunAuth{Required: auth.Required, Public: s.Security != nil && s.Security.Public, Scopes: auth.Scopes}
}

type DryRunRequest struct {
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Hostname   string            `json:"hostname,omitempty"`
	HostSource string            `json:"host_source,omitempty"`
	Headers    map[string]string `json:"headers"`
	Body       any               `json:"body"`
	Auth       DryRunAuth        `json:"auth"`
	Output     CatalogOutput     `json:"output"`
}

type DryRunAuth struct {
	Required bool     `json:"required"`
	Public   bool     `json:"public"`
	Scopes   []string `json:"scopes,omitempty"`
}

func buildDryRunRequest(ctx context.Context, s CommandSpec, hostname, hostSource, path string, body any, opts ClientOptions) (DryRunRequest, error) {
	req, bodyBytes, _, err := resolveRequest(ctx, hostname, s.Method, path, body, opts)
	if err != nil {
		return DryRunRequest{}, err
	}
	return DryRunRequest{
		Method:     req.Method,
		URL:        redactDebugURL(req.URL, opts.sensitiveQueryParams),
		Hostname:   hostname,
		HostSource: hostSource,
		Headers:    redactedDryRunHeaders(req.Header),
		Body:       redactedDryRunBody(req.Header.Get("Content-Type"), bodyBytes, sensitiveBodyFields(s)),
		Auth:       dryRunAuthForSpec(s),
		Output: CatalogOutput{
			ListPath:          s.Output.ListPath,
			DefaultColumns:    append([]string(nil), s.Output.DefaultColumns...),
			ResponseMediaType: s.Output.ResponseMediaType,
			Pagination:        catalogPagination(s.Output.Pagination),
			Streaming:         catalogStreaming(s.Output.Streaming),
		},
	}, nil
}

func writeDryRun(out DryRunRequest, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
