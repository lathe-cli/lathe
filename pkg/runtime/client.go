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
	Auth        Authenticator
	RefreshAuth func(context.Context) (Authenticator, error)
	Transport   http.RoundTripper
	Insecure    bool
	Timeout     time.Duration
	Headers     map[string]string
	Debug       bool
	MaxRetries  int
	UserAgent   string
	Accept      string

	sensitiveQueryParams map[string]bool
	sensitivePath        bool
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
		transport = &debugTransport{inner: transport, sensitiveQueryParams: opts.sensitiveQueryParams, sensitivePath: opts.sensitivePath, streaming: streaming}
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
	Fields url.Values
	Files  map[string]string
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
			if err := w.WriteField(name, value); err != nil {
				return nil, "", fmt.Errorf("write multipart field %q: %w", name, err)
			}
		}
	}
	fileNames := make([]string, 0, len(form.Files))
	for name := range form.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		path := form.Files[name]
		data, err := ReadBody(path)
		if err != nil {
			return nil, "", fmt.Errorf("read multipart file %q: %w", path, err)
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name": name, "filename": filepath.Base(path),
		}))
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		header.Set("Content-Type", contentType)
		part, partErr := w.CreatePart(header)
		if partErr == nil {
			_, partErr = part.Write(data)
		}
		if partErr != nil {
			return nil, "", fmt.Errorf("write multipart file %q: %w", path, partErr)
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart body: %w", err)
	}
	return body.Bytes(), w.FormDataContentType(), nil
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
		req.Header.Set(k, v)
	}
	return req, nil
}

func doRawFullOnce(req *http.Request, opts ClientOptions, consume responseConsumer) (*RawResult, error) {
	method := req.Method
	u := redactDebugURL(req.URL, opts.sensitiveQueryParams, opts.sensitivePath)
	resp, err := httpClient(opts, consume != nil).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, u, redactClientError(err, opts.sensitiveQueryParams, opts.sensitivePath))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, &HTTPError{
			Method: method,
			URL:    u,
			Status: resp.StatusCode,
			Body:   data,
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
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &RawResult{Body: data, StatusCode: resp.StatusCode, Header: resp.Header}, nil
}

func redactClientError(err error, sensitive map[string]bool, sensitivePath bool) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	redacted.URL = redactDebugURLString(redacted.URL, sensitive, sensitivePath)
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
	Method string
	URL    string
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Status)
}
