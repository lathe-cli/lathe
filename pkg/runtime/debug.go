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
	"regexp"
	"strings"
	"time"
)

const (
	maxDebugReqBody  = 1024
	maxDebugRespBody = 4096
)

var (
	authorizationPattern  = regexp.MustCompile(`(?i)\b(Basic|Bearer)[ \t]+[A-Za-z0-9._~+/=-]+`)
	debugAssignmentPrefix = regexp.MustCompile(`[[:alnum:]_.-]+[ \t]*[=:][ \t]*`)
	debugURLPattern       = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
)

type debugTransport struct {
	inner                http.RoundTripper
	sensitiveQueryParams map[string]bool
	sensitivePath        bool
	streaming            bool
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	fmt.Fprintf(os.Stderr, "> %s %s\n", req.Method, redactDebugURL(req.URL, d.sensitiveQueryParams, d.sensitivePath))
	for k, vs := range req.Header {
		fmt.Fprintf(os.Stderr, "> %s: %s\n", k, redactDebugHeader(k, strings.Join(vs, ", "), d.sensitiveQueryParams, d.sensitivePath))
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
		fmt.Fprintf(os.Stderr, "< %s: %s\n", k, redactDebugHeader(k, strings.Join(vs, ", "), d.sensitiveQueryParams, d.sensitivePath))
	}
	if isTextContent(resp.Header.Get("Content-Type")) && (!d.streaming || resp.StatusCode < 200 || resp.StatusCode >= 300) {
		body, restored := peekBody(resp.Body, maxDebugRespBody)
		resp.Body = restored
		dumpBody(os.Stderr, "<", redactDebugBody(resp.Header.Get("Content-Type"), body), maxDebugRespBody)
	}
	fmt.Fprintln(os.Stderr)

	return resp, nil
}

func redactDebugURL(u *url.URL, sensitive map[string]bool, sensitivePath bool) string {
	if u == nil {
		return ""
	}
	redacted := *u
	if sensitivePath {
		redacted.Path = "/***"
		redacted.RawPath = ""
	}
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

func redactDebugURLString(raw string, sensitive map[string]bool, sensitivePath bool) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid URL>"
	}
	return redactDebugURL(parsed, sensitive, sensitivePath)
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

func redactDebugHeader(name, value string, sensitive map[string]bool, sensitivePath bool) string {
	if strings.EqualFold(name, "location") || strings.EqualFold(name, "content-location") {
		return redactDebugURLString(value, sensitive, sensitivePath)
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
			if text, ok := child.(string); ok {
				redacted := redactDebugText(text)
				if redacted != text {
					tv[k] = redacted
					changed = true
				}
				continue
			}
			if redactDebugJSONAt(child, append(path, k)) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for i, child := range tv {
			if text, ok := child.(string); ok {
				redacted := redactDebugText(text)
				if redacted != text {
					tv[i] = redacted
					changed = true
				}
				continue
			}
			if redactDebugJSONAt(child, path) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func redactAuthorization(s string) string {
	return authorizationPattern.ReplaceAllString(s, "$1 ***")
}

func isDebugEnvVarPair(path []string, v map[string]any) bool {
	if len(path) == 0 || !isDebugEnvVarContainer(path[len(path)-1]) {
		return false
	}
	var hasKey, hasValue bool
	for name := range v {
		switch strings.ToLower(name) {
		case "key", "name":
			hasKey = true
		case "value":
			hasValue = true
		}
	}
	return hasKey && hasValue
}

func isDebugEnvVarContainer(name string) bool {
	n := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	return n == "env" || n == "envvars" || n == "environmentvariables"
}

func redactDebugText(s string) string {
	s = redactAuthorization(s)
	s = debugURLPattern.ReplaceAllStringFunc(s, func(raw string) string {
		return redactDebugURLString(raw, nil, false)
	})
	matches := debugAssignmentPrefix.FindAllStringIndex(s, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start, valueStart := matches[i][0], matches[i][1]
		prefix := s[start:valueStart]
		separator := strings.IndexAny(prefix, "=:")
		if separator < 0 || !isSensitiveDebugName(strings.TrimSpace(prefix[:separator])) {
			continue
		}
		valueEnd := len(s)
		if boundary := strings.IndexAny(s[valueStart:], "&;\n\r"); boundary >= 0 {
			valueEnd = valueStart + boundary
		}
		s = s[:valueStart] + "***" + s[valueEnd:]
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
