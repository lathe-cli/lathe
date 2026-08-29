package runtime

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type debugTransport struct {
	inner                http.RoundTripper
	sensitiveQueryParams map[string]bool
	streaming            bool
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	fmt.Fprintf(os.Stderr, "> %s request host=%s\n\n", req.Method, req.URL.Host)

	start := time.Now()
	resp, err := d.inner.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "< request failed (%s)\n\n", elapsed)
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "< HTTP %d (%s)\n", resp.StatusCode, elapsed)
	fmt.Fprintln(os.Stderr)

	return resp, nil
}

func redactDebugURL(u *url.URL, sensitive map[string]bool) string {
	if u == nil {
		return ""
	}
	redacted := *u
	redacted.RawQuery = redactDebugQuery(redacted.RawQuery, sensitive)
	return redacted.Redacted()
}

func redactDebugQuery(raw string, sensitive map[string]bool) string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '&' || r == ';' })
	changed := false
	for i, part := range parts {
		name, _, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if sensitive[strings.ToLower(decoded)] || isSensitiveDebugQueryName(decoded) {
			parts[i] = name + "=" + url.QueryEscape("***")
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return strings.Join(parts, "&")
}

func isSensitiveDebugQueryName(name string) bool {
	n := sensitiveNameKey(name)
	switch n {
	case "authorization", "proxyauthorization", "token", "key", "apikey", "xapikey", "accesstoken", "idtoken", "refreshtoken", "authtoken", "oauthtoken", "bearertoken", "privatekey", "sessiontoken", "securitytoken", "xamzsecuritytoken", "sig":
		return true
	}
	for _, marker := range []string{"secret", "password", "credential", "signature"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

func redactDebugURLString(raw string, sensitive map[string]bool) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid URL>"
	}
	return redactDebugURL(parsed, sensitive)
}

func redactDebugHeader(name, value string, sensitive map[string]bool) string {
	if strings.EqualFold(name, "location") || strings.EqualFold(name, "content-location") {
		return redactDebugURLString(value, sensitive)
	}
	if isSensitiveDebugName(name) {
		return "***"
	}
	return value
}

func isSensitiveDebugName(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "sig":
		return true
	}
	for _, marker := range []string{"token", "key", "secret", "password", "credential", "signature"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

func redactDebugBody(contentType string, body []byte, sensitive map[string]bool) []byte {
	if len(body) == 0 {
		return body
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	if mt == "application/json" {
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			if redactDebugJSON(v, sensitive) {
				redacted, err := json.Marshal(v)
				if err == nil {
					return redacted
				}
			}
		}
	}
	return []byte(redactDebugText(string(body)))
}

func redactDebugJSON(v any, sensitive map[string]bool) bool {
	return redactDebugJSONAt(v, nil, sensitive)
}

func redactDebugJSONAt(v any, path []string, sensitive map[string]bool) bool {
	switch tv := v.(type) {
	case map[string]any:
		changed := false
		envVarPair := isDebugEnvVarPair(path, tv)
		for k, child := range tv {
			if len(path) == 0 && sensitive[k] {
				tv[k] = "***"
				changed = true
				continue
			}
			if envVarPair && strings.EqualFold(k, "value") {
				tv[k] = "***"
				changed = true
				continue
			}
			if envVarPair && strings.EqualFold(k, "key") {
				continue
			}
			if isSensitiveDebugName(k) {
				tv[k] = "***"
				changed = true
				continue
			}
			if redactDebugJSONAt(child, append(path, k), sensitive) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range tv {
			if redactDebugJSONAt(child, path, sensitive) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func sensitiveBodyFields(s CommandSpec) map[string]bool {
	out := map[string]bool{}
	for _, p := range s.Params {
		if p.In == InBody && isSensitiveStringParam(p) {
			out[p.Name] = true
		}
	}
	if s.RequestBody == nil || s.RequestBody.Schema == nil {
		return out
	}
	for name, schema := range s.RequestBody.Schema.Properties {
		if schemaUsesPasswordFormat(schema, map[*SchemaSpec]bool{}) {
			out[name] = true
		}
	}
	return out
}

func schemaUsesPasswordFormat(s *SchemaSpec, visited map[*SchemaSpec]bool) bool {
	if s == nil || visited[s] {
		return false
	}
	visited[s] = true
	if strings.EqualFold(s.Format, "password") || schemaUsesPasswordFormat(s.Items, visited) {
		return true
	}
	for _, variants := range [][]*SchemaSpec{s.AnyOf, s.OneOf, s.AllOf} {
		for _, variant := range variants {
			if schemaUsesPasswordFormat(variant, visited) {
				return true
			}
		}
	}
	return false
}

func isDebugEnvVarPair(path []string, v map[string]any) bool {
	if len(path) == 0 || !isDebugEnvVarContainer(path[len(path)-1]) {
		return false
	}
	_, hasKey := v["key"]
	_, hasValue := v["value"]
	return hasKey && hasValue
}

func isDebugEnvVarContainer(name string) bool {
	n := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	return n == "env" || n == "envvars" || n == "environmentvariables"
}

func redactDebugText(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '&' || r == ';' || r == '\n' || r == '\r'
	})
	for _, field := range fields {
		name, value, ok := strings.Cut(field, "=")
		if !ok || !isSensitiveDebugName(strings.TrimSpace(name)) || value == "" {
			continue
		}
		s = strings.ReplaceAll(s, field, name+"=***")
	}
	return s
}
