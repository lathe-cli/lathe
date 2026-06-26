package runtime

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"
)

type retryTransport struct {
	inner           http.RoundTripper
	maxRetries      int
	sleepFn         func(time.Duration)
	debug           bool
	safeMethodsOnly bool
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if attempt > 0 {
			wait := retryBackoff(attempt, resp)
			if t.debug {
				reason := "backoff"
				if resp != nil {
					if ra := resp.Header.Get("Retry-After"); ra != "" {
						reason = "Retry-After=" + ra
					}
				}
				fmt.Fprintf(os.Stderr, "[retry %d/%d] %s - waiting %s (%s)\n", attempt, t.maxRetries, resp.Status, wait, reason)
			}
			if t.sleepFn != nil {
				t.sleepFn(wait)
			} else {
				time.Sleep(wait)
			}
			if req.GetBody != nil {
				req.Body, err = req.GetBody()
				if err != nil {
					return nil, err
				}
			}
		}
		resp, err = t.inner.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if !isRetryable(req.Method, resp.StatusCode, t.safeMethodsOnly) {
			return resp, nil
		}
		if attempt < t.maxRetries {
			resp.Body.Close()
		}
	}
	return resp, nil
}

func isRetryable(method string, status int, safeMethodsOnly bool) bool {
	switch status {
	case 429, 500, 502, 503, 504:
	default:
		return false
	}
	if !safeMethodsOnly {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func retryBackoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 && secs <= 300 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
}
