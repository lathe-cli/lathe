package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	timeout := opts.Timeout
	if timeout == 0 {
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
		transport = &debugTransport{inner: transport}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

type RawResult struct {
	Body       []byte
	StatusCode int
	Header     http.Header
}

func DoRawFull(ctx context.Context, hostname, method, path string, body any, opts ClientOptions) (*RawResult, error) {
	base, err := BaseURL(hostname)
	if err != nil {
		return nil, err
	}
	u := base + path

	bodyBytes, contentType, err := encodeRequestBody(body)
	if err != nil {
		return nil, err
	}

	result, err := doRawFullOnce(ctx, method, u, bodyBytes, contentType, opts)
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
	return doRawFullOnce(ctx, method, u, bodyBytes, contentType, opts)
}

func encodeRequestBody(body any) ([]byte, string, error) {
	if body != nil {
		switch b := body.(type) {
		case []byte:
			return b, "application/json", nil
		case url.Values:
			return []byte(b.Encode()), "application/x-www-form-urlencoded", nil
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

func doRawFullOnce(ctx context.Context, method, u string, body []byte, contentType string, opts ClientOptions) (*RawResult, error) {
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

	resp, err := HTTPClient(opts).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{
			Method: method,
			URL:    u,
			Status: resp.StatusCode,
			Body:   data,
		}
	}
	return &RawResult{Body: data, StatusCode: resp.StatusCode, Header: resp.Header}, nil
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
	snippet := string(e.Body)
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, strings.TrimSpace(snippet))
}
