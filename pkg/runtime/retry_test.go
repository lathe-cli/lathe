package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryTransport_RetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	rt := &retryTransport{
		inner:      http.DefaultTransport,
		maxRetries: 3,
		sleepFn:    func(time.Duration) {},
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/x", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestRetryTransport_RetriesOn5xx(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt32(&calls, 1)
				if n == 1 {
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(200)
			}))
			defer srv.Close()

			rt := &retryTransport{
				inner:      http.DefaultTransport,
				maxRetries: 1,
				sleepFn:    func(time.Duration) {},
			}
			req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

func TestHTTPClient_DefaultRetriesSkipPost(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) > 1 {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL, strings.NewReader(`{"key":"val"}`))
	resp, err := HTTPClient(ClientOptions{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestRetryTransport_NoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(404)
	}))
	defer srv.Close()

	rt := &retryTransport{
		inner:      http.DefaultTransport,
		maxRetries: 3,
		sleepFn:    func(time.Duration) {},
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 404)", got)
	}
}

func TestRetryTransport_StopsAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	rt := &retryTransport{
		inner:      http.DefaultTransport,
		maxRetries: 2,
		sleepFn:    func(time.Duration) {},
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503 after exhausting retries", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (1 initial + 2 retries)", got)
	}
}

func TestRetryTransport_ReplaysBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	rt := &retryTransport{
		inner:      http.DefaultTransport,
		maxRetries: 2,
		sleepFn:    func(time.Duration) {},
	}
	body := strings.NewReader(`{"key":"val"}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL, body)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"key":"val"}`)), nil
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if len(bodies) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(bodies))
	}
	for i, b := range bodies {
		if b != `{"key":"val"}` {
			t.Errorf("attempt %d body = %q", i, b)
		}
	}
}

func TestRetryTransport_RespectsRetryAfter(t *testing.T) {
	var slept time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
	}))
	defer srv.Close()

	rt := &retryTransport{
		inner:      http.DefaultTransport,
		maxRetries: 1,
		sleepFn:    func(d time.Duration) { slept = d },
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, _ := rt.RoundTrip(req)
	resp.Body.Close()
	if slept != 7*time.Second {
		t.Errorf("slept = %v, want 7s (Retry-After)", slept)
	}
}

func TestRetryTransport_CancellationStopsBackoff(t *testing.T) {
	called := make(chan struct{}, 1)
	rt := &retryTransport{
		inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called <- struct{}{}
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}),
		maxRetries: 1,
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
	errCh := make(chan error, 1)
	go func() {
		_, err := rt.RoundTrip(req)
		errCh <- err
	}()
	<-called
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("retry backoff ignored cancellation")
	}
}

func TestRetryBackoff_Exponential(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
	}
	for _, tc := range cases {
		got := retryBackoff(tc.attempt, nil)
		if got != tc.want {
			t.Errorf("backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}
