package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultPollTimeout = 5 * time.Minute
	pollInitBackoff    = 1 * time.Second
	pollMaxBackoff     = 30 * time.Second
)

func PollUntilDone(ctx context.Context, hostname, location string, opts ClientOptions, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = DefaultPollTimeout
	}
	pollCtx, cancel := context.WithTimeoutCause(ctx, timeout, newAPIError(fmt.Errorf("polling timed out after %s: %w", timeout, context.DeadlineExceeded), 0))
	defer cancel()
	pollError := func() error {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return newAPIError(err, 0)
			}
			return err
		}
		return context.Cause(pollCtx)
	}
	base, err := baseURL(hostname)
	if err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch strings.ToLower(u.Scheme) {
		case "http":
			return "80"
		case "https":
			return "443"
		default:
			return ""
		}
	}
	baseURL.Path += "/"
	if baseURL.RawPath != "" {
		baseURL.RawPath += "/"
	}
	origin := baseURL.Scheme + "://" + baseURL.Host
	opts.checkRedirect = func(req *http.Request, _ []*http.Request) error {
		if !strings.EqualFold(req.URL.Scheme, baseURL.Scheme) || !strings.EqualFold(req.URL.Hostname(), baseURL.Hostname()) || port(req.URL) != port(baseURL) {
			return fmt.Errorf("cross-host polling redirect %q", redactDebugURL(req.URL, opts.sensitiveQueryParams))
		}
		return nil
	}
	backoff := pollInitBackoff

	for {
		if pollCtx.Err() != nil {
			return nil, pollError()
		}

		loc, err := url.Parse(location)
		if err != nil {
			return nil, newAPIError(fmt.Errorf("parse polling location: %w", redactClientError(err, opts.sensitiveQueryParams)), 0)
		}
		resolved := baseURL.ResolveReference(loc)
		if !strings.EqualFold(resolved.Scheme, baseURL.Scheme) || !strings.EqualFold(resolved.Hostname(), baseURL.Hostname()) || port(resolved) != port(baseURL) {
			return nil, newAPIError(fmt.Errorf("cross-host polling location %q", redactDebugURLString(location, opts.sensitiveQueryParams)), 0)
		}
		location = resolved.RequestURI()
		baseURL = resolved
		r, err := DoRawFull(pollCtx, origin, "GET", location, nil, opts)
		if err != nil {
			if pollCtx.Err() != nil {
				return nil, pollError()
			}
			return nil, err
		}

		if r.StatusCode != http.StatusAccepted {
			return r.Body, nil
		}

		if loc := r.Header.Get("Location"); loc != "" {
			location = loc
		}

		select {
		case <-pollCtx.Done():
			return nil, pollError()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > pollMaxBackoff {
			backoff = pollMaxBackoff
		}
	}
}
