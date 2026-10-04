package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ClientOptions struct {
	Auth               Authenticator
	RefreshAuth        func(context.Context) (Authenticator, error)
	Transport          http.RoundTripper
	Insecure           bool
	Timeout            time.Duration
	Headers            map[string]string
	Debug              bool
	MaxRetries         int
	UserAgent          string
	Accept             string
	binaryContentGuard bool

	sensitiveQueryParams map[string]bool
	checkRedirect        func(*http.Request, []*http.Request) error
}

// BaseURL normalizes a user-facing hostname into an absolute URL base.
// Accepts: "host", "host:port", "https://host", "https://host:port".
// Default scheme is https; no default port (standard 443).
func BaseURL(hostname string) (string, error) {
	h := strings.TrimSpace(hostname)
	if h == "" {
		return "", fmt.Errorf("empty hostname")
	}
	if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
		h = "https://" + h
	}
	return strings.TrimRight(h, "/"), nil
}

// HTTPClient returns an http.Client configured per opts.
func HTTPClient(opts ClientOptions) *http.Client {
	return httpClient(opts, false)
}

func httpClient(opts ClientOptions, streaming bool) *http.Client {
	timeout := opts.Timeout
	if timeout == 0 && !streaming {
		timeout = 30 * time.Second
	}
	transport := opts.Transport
	if transport == nil {
		var tlsCfg *tls.Config
		if opts.Insecure {
			tlsCfg = &tls.Config{InsecureSkipVerify: true}
		}
		transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	maxRetries := opts.MaxRetries
	safeMethodsOnly := maxRetries == 0
	if safeMethodsOnly {
		maxRetries = 3
	}
	if maxRetries > 0 {
		transport = &retryTransport{inner: transport, maxRetries: maxRetries, debug: opts.Debug, safeMethodsOnly: safeMethodsOnly}
	}
	if opts.Debug {
		transport = &debugTransport{inner: transport, sensitiveQueryParams: opts.sensitiveQueryParams, streaming: streaming}
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: opts.checkRedirect,
	}
}

type RawResult struct {
	Body       []byte
	StatusCode int
	Header     http.Header
}

func DoRawFull(ctx context.Context, hostname, method, path string, body any, opts ClientOptions) (*RawResult, error) {
	return doRawFull(ctx, hostname, method, path, body, opts, nil)
}

func doRawFull(ctx context.Context, hostname, method, path string, body any, opts ClientOptions, stream io.Writer) (*RawResult, error) {
	var consume responseConsumer
	if stream != nil {
		consume = func(r io.Reader) ([]byte, error) {
			_, err := io.Copy(stream, r)
			if err != nil {
				return nil, fmt.Errorf("stream response: %w", err)
			}
			return nil, nil
		}
	}
	return doRawFullConsume(ctx, hostname, method, path, body, opts, consume)
}

type responseConsumer func(io.Reader) ([]byte, error)

type multipartForm struct {
	Fields       url.Values
	Files        map[string][]string
	ContentTypes map[string]string
}

func doRawFullConsume(ctx context.Context, hostname, method, path string, body any, opts ClientOptions, consume responseConsumer) (*RawResult, error) {
	req, bodyBytes, contentType, err := resolveRequest(ctx, hostname, method, path, body, opts)
	if err != nil {
		return nil, err
	}
	u := req.URL.String()

	result, err := doRawFullOnce(req, opts, consume)
	if err == nil {
		return result, nil
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized || opts.RefreshAuth == nil {
		return nil, err
	}
	auth, refreshErr := opts.RefreshAuth(ctx)
	if refreshErr != nil {
		return nil, fmt.Errorf("refresh auth after 401: %w", refreshErr)
	}
	opts.Auth = auth
	opts.RefreshAuth = nil
	req, err = newRequest(ctx, method, u, bodyBytes, contentType, opts)
	if err != nil {
		return nil, err
	}
	return doRawFullOnce(req, opts, consume)
}

func encodeRequestBody(body any) ([]byte, string, error) {
	if body != nil {
		switch b := body.(type) {
		case []byte:
			return b, "application/json", nil
		case url.Values:
			return []byte(b.Encode()), "application/x-www-form-urlencoded", nil
		case multipartForm:
			return encodeMultipartForm(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return nil, "", fmt.Errorf("marshal request body: %w", err)
			}
			return raw, "application/json", nil
		}
	}
	return nil, "", nil
}

func encodeMultipartForm(form multipartForm) ([]byte, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fieldNames := make([]string, 0, len(form.Fields))
	for name := range form.Fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	for _, name := range fieldNames {
		for _, value := range form.Fields[name] {
			contentType := partContentType(form.ContentTypes[name], "text/plain", []byte(value))
			if err := writeMultipartText(w, name, value, contentType); err != nil {
				return nil, "", err
			}
		}
	}
	fileNames := make([]string, 0, len(form.Files))
	for name := range form.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		for _, path := range form.Files[name] {
			data, err := ReadBody(path)
			if err != nil {
				return nil, "", fmt.Errorf("read multipart file %q: %w", path, err)
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", contentDisposition(name, filepath.Base(path)))
			header.Set("Content-Type", filePartContentType(form.ContentTypes[name], path, data))
			part, partErr := w.CreatePart(header)
			if partErr == nil {
				_, partErr = part.Write(data)
			}
			if partErr != nil {
				return nil, "", fmt.Errorf("write multipart file %q: %w", path, partErr)
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart body: %w", err)
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

func writeMultipartText(w *multipart.Writer, name, value, contentType string) error {
	if contentType == "text/plain" {
		if err := w.WriteField(name, value); err != nil {
			return fmt.Errorf("write multipart field %q: %w", name, err)
		}
		return nil
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", contentDisposition(name, ""))
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("write multipart field %q: %w", name, err)
	}
	if _, err := part.Write([]byte(value)); err != nil {
		return fmt.Errorf("write multipart field %q: %w", name, err)
	}
	return nil
}

func filePartContentType(declared, path string, data []byte) string {
	if strings.TrimSpace(declared) == "" {
		if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
			return contentType
		}
		return "application/octet-stream"
	}
	return partContentType(declared, "application/octet-stream", data)
}

func contentDisposition(name, filename string) string {
	disposition := fmt.Sprintf("form-data; name=\"%s\"", escapeQuotes(name))
	if filename != "" {
		disposition += fmt.Sprintf("; filename=\"%s\"", escapeQuotes(filename))
	}
	return disposition
}

func escapeQuotes(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "%0D", "\n", "%0A").Replace(s)
}

func partContentType(declared, emptyFallback string, data []byte) string {
	elements := splitMediaTypes(declared)
	if len(elements) == 0 {
		return emptyFallback
	}
	if len(elements) == 1 {
		if mediaType := mediaTypeValue(elements[0]); mediaType != "" && !strings.Contains(mediaType, "*") {
			return elements[0]
		}
	}
	sniffedRaw := http.DetectContentType(data)
	sniffed := mediaTypeValue(sniffedRaw)
	for _, element := range elements {
		mediaType := mediaTypeValue(element)
		if mediaType != "" && mediaType == sniffed && !strings.Contains(mediaType, "*") {
			return element
		}
	}
	for _, element := range elements {
		if wildcardMatches(mediaTypeValue(element), sniffed) {
			return sniffedRaw
		}
	}
	for _, element := range elements {
		mediaType := mediaTypeValue(element)
		if mediaType != "" && !strings.Contains(mediaType, "*") {
			return element
		}
	}
	return "application/octet-stream"
}

func splitMediaTypes(declared string) []string {
	if strings.TrimSpace(declared) == "" {
		return nil
	}
	parts := strings.Split(declared, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mediaTypeValue(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return mediaType
}

func wildcardMatches(pattern, concrete string) bool {
	if pattern == "" || concrete == "" || !strings.Contains(pattern, "*") {
		return false
	}
	if pattern == "*/*" {
		return true
	}
	patternType, patternSub, ok := strings.Cut(pattern, "/")
	concreteType, _, concreteOK := strings.Cut(concrete, "/")
	return ok && concreteOK && patternSub == "*" && patternType == concreteType
}

func isMultipartMediaType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "multipart/form-data"
}

func resolveRequest(ctx context.Context, hostname, method, path string, body any, opts ClientOptions) (*http.Request, []byte, string, error) {
	base, err := BaseURL(hostname)
	if err != nil {
		return nil, nil, "", err
	}
	bodyBytes, contentType, err := encodeRequestBody(body)
	if err != nil {
		return nil, nil, "", err
	}
	req, err := newRequest(ctx, method, base+path, bodyBytes, contentType, opts)
	if err != nil {
		return nil, nil, "", err
	}
	return req, bodyBytes, contentType, nil
}

func newRequest(ctx context.Context, method, u string, body []byte, contentType string, opts ClientOptions) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if opts.Auth != nil {
		if err := opts.Auth.Apply(req); err != nil {
			return nil, fmt.Errorf("apply auth: %w", err)
		}
	}
	accept := opts.Accept
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range opts.Headers {
		if strings.EqualFold(k, "Cookie") {
			if prev := req.Header.Get("Cookie"); prev != "" {
				v = prev + "; " + v
			}
		}
		req.Header.Set(k, v)
	}
	return req, nil
}

func doRawFullOnce(req *http.Request, opts ClientOptions, consume responseConsumer) (*RawResult, error) {
	method := req.Method
	resp, err := httpClient(opts, consume != nil).Do(req)
	if err != nil {
		cause := redactClientError(err, opts.sensitiveQueryParams)
		if errors.Is(cause, context.Canceled) {
			return nil, cause
		}
		return nil, newAPIError(cause, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, newAPIError(fmt.Errorf("read response: %w", err), resp.StatusCode)
		}
		return nil, &HTTPError{
			Method:      method,
			URL:         redactDebugURL(req.URL, opts.sensitiveQueryParams),
			Status:      resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			Body:        data,
		}
	}
	if opts.binaryContentGuard {
		if media, reject := rejectBinaryContentType(opts.Accept, resp.Header.Get("Content-Type")); reject {
			return nil, binaryContentTypeError(media, resp.StatusCode)
		}
	}
	if consume != nil {
		data, err := consume(resp.Body)
		if err != nil {
			return nil, err
		}
		return &RawResult{Body: data, StatusCode: resp.StatusCode, Header: resp.Header}, nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newAPIError(fmt.Errorf("read response: %w", err), resp.StatusCode)
	}
	return &RawResult{Body: data, StatusCode: resp.StatusCode, Header: resp.Header}, nil
}

func rejectBinaryContentType(declared, actual string) (string, bool) {
	got, _, err := mime.ParseMediaType(actual)
	if err != nil {
		return "", false
	}
	got = strings.ToLower(got)
	if !jsonOrHTMLMedia(got) || declaredAllowsStoredMedia(declared) {
		return "", false
	}
	want, _, err := mime.ParseMediaType(declared)
	if err == nil && jsonOrHTMLMedia(strings.ToLower(want)) {
		return "", false
	}
	return got, true
}

func declaredAllowsStoredMedia(declared string) bool {
	base, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return strings.Contains(declared, "*")
	}
	base = strings.ToLower(base)
	return base == "application/octet-stream" || strings.Contains(base, "*")
}

func jsonOrHTMLMedia(base string) bool {
	return base == "text/html" || base == "application/json" || strings.HasSuffix(base, "+json")
}

func binaryContentTypeError(media string, status int) error {
	le := newAPIError(errors.New("unexpected response media type"), status)
	le.Detail = sanitizeErrorDetail("unexpected response media type " + media)
	return le
}

func redactClientError(err error, sensitive map[string]bool) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	redacted.URL = redactDebugURLString(redacted.URL, sensitive)
	if urlErr.Err != nil && strings.HasPrefix(urlErr.Err.Error(), "failed to parse Location header ") {
		redacted.Err = errors.New("failed to parse Location header")
	}
	return &redacted
}

func DoRaw(ctx context.Context, hostname, method, path string, body any, opts ClientOptions) ([]byte, error) {
	r, err := DoRawFull(ctx, hostname, method, path, body, opts)
	if err != nil {
		var he *HTTPError
		if errors.As(err, &he) {
			return he.Body, err
		}
		return nil, err
	}
	return r.Body, nil
}

type HTTPError struct {
	Method      string
	URL         string
	Status      int
	ContentType string
	Body        []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s request failed with HTTP %d", e.Method, e.Status)
}
