package runtime

import (
	"context"
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
	base, err := BaseURL(hostname)
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
	deadline := time.Now().Add(timeout)
	backoff := pollInitBackoff

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("polling timed out after %s", timeout)
		}

		loc, err := url.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("parse polling location: %w", err)
		}
		if loc.IsAbs() || loc.Host != "" {
			if !strings.EqualFold(loc.Scheme, baseURL.Scheme) || !strings.EqualFold(loc.Hostname(), baseURL.Hostname()) || port(loc) != port(baseURL) {
				return nil, fmt.Errorf("cross-host polling location %q", location)
			}
			location = loc.RequestURI()
		}
		r, err := DoRawFull(ctx, hostname, "GET", location, nil, opts)
		if err != nil {
			return nil, err
		}

		if r.StatusCode != http.StatusAccepted {
			return r.Body, nil
		}

		if loc := r.Header.Get("Location"); loc != "" {
			location = loc
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > pollMaxBackoff {
			backoff = pollMaxBackoff
		}
	}
}
