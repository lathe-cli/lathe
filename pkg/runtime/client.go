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
	Auth       Authenticator
	Transport  http.RoundTripper
	Insecure   bool
	Timeout    time.Duration
	Headers    map[string]string
	Debug      bool
	MaxRetries int
	UserAgent  string
	Accept     string
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

	var reader io.Reader
	contentType := ""
	if body != nil {
		switch b := body.(type) {
		case []byte:
			reader = bytes.NewReader(b)
			contentType = "application/json"
		case url.Values:
			reader = strings.NewReader(b.Encode())
			contentType = "application/x-www-form-urlencoded"
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return nil, fmt.Errorf("marshal request body: %w", err)
			}
			reader = bytes.NewReader(raw)
			contentType = "application/json"
		}
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
