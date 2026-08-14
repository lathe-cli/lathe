package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxDebugReqBody  = 1024
	maxDebugRespBody = 4096
)

type debugTransport struct {
	inner                http.RoundTripper
	sensitiveQueryParams map[string]bool
	streaming            bool
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	fmt.Fprintf(os.Stderr, "> %s %s\n", req.Method, redactDebugURL(req.URL, d.sensitiveQueryParams))
	for k, vs := range req.Header {
		fmt.Fprintf(os.Stderr, "> %s: %s\n", k, redactDebugHeader(k, strings.Join(vs, ", "), d.sensitiveQueryParams))
	}
	if req.Body != nil && isTextContent(req.Header.Get("Content-Type")) {
		body, restored := peekBody(req.Body, maxDebugReqBody)
		req.Body = restored
		dumpBody(os.Stderr, ">", redactDebugBody(req.Header.Get("Content-Type"), body), maxDebugReqBody)
	}
	fmt.Fprintln(os.Stderr)

	start := time.Now()
	resp, err := d.inner.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "< error: %v (%s)\n\n", err, elapsed)
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "< %s (%s)\n", resp.Status, elapsed)
	for k, vs := range resp.Header {
		fmt.Fprintf(os.Stderr, "< %s: %s\n", k, redactDebugHeader(k, strings.Join(vs, ", "), d.sensitiveQueryParams))
	}
	if isTextContent(resp.Header.Get("Content-Type")) && (!d.streaming || resp.StatusCode < 200 || resp.StatusCode >= 300) {
		body, restored := peekBody(resp.Body, maxDebugRespBody)
		resp.Body = restored
		dumpBody(os.Stderr, "<", redactDebugBody(resp.Header.Get("Content-Type"), body), maxDebugRespBody)
	}
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

type bodyReader struct {
	io.Reader
	io.Closer
}

func isTextContent(ct string) bool {
	if ct == "" {
		return false
	}
	mt, _, _ := mime.ParseMediaType(ct)
	switch {
	case strings.HasPrefix(mt, "text/"):
		return true
	case mt == "application/json", mt == "application/xml", mt == "application/x-www-form-urlencoded":
		return true
	}
	return false
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

func redactDebugBody(contentType string, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	if mt == "application/json" {
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			if redactDebugJSON(v) {
				redacted, err := json.Marshal(v)
				if err == nil {
					return redacted
				}
			}
		}
	}
	return []byte(redactDebugText(string(body)))
}

func redactDebugJSON(v any) bool {
	return redactDebugJSONAt(v, nil)
}

func redactDebugJSONAt(v any, path []string) bool {
	switch tv := v.(type) {
	case map[string]any:
		changed := false
		envVarPair := isDebugEnvVarPair(path, tv)
		for k, child := range tv {
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
			if redactDebugJSONAt(child, append(path, k)) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range tv {
			if redactDebugJSONAt(child, path) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
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

func peekBody(body io.ReadCloser, max int) ([]byte, io.ReadCloser) {
	peeked, err := io.ReadAll(io.LimitReader(body, int64(max)+1))
	restored := bodyReader{Reader: io.MultiReader(bytes.NewReader(peeked), body), Closer: body}
	if err != nil {
		return nil, restored
	}
	return peeked, restored
}

func dumpBody(w io.Writer, prefix string, body []byte, max int) {
	if len(body) == 0 {
		return
	}
	if len(body) > max {
		fmt.Fprintf(w, "%s [body at least %d bytes, showing first %d]\n", prefix, len(body), max)
		fmt.Fprintln(w, string(body[:max]))
	} else {
		fmt.Fprintf(w, "%s [body %d bytes]\n", prefix, len(body))
		fmt.Fprintln(w, string(body))
	}
}
