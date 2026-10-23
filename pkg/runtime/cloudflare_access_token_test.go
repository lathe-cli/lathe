package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/lathe-cli/lathe/pkg/config"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func accessJWT(expires int64) string {
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expires))) + ".c2lnbmF0dXJl"
}

func fakeAccessCommand(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("ACCESS_TEST_CALLS", log)
	t.Setenv("ACCESS_TEST_TOKEN", accessJWT(time.Now().Add(time.Hour).Unix()))
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$ACCESS_TEST_CALLS\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cloudflared"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return log
}

func accessCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' })
}

func TestAccessTokenFailuresDoNotSendRequests(t *testing.T) {
	for _, tc := range []struct{ name, script, token string }{
		{"empty", "exit 0", ""},
		{"failed", "printf '%s' \"$ACCESS_TEST_TOKEN\"; printf '%s' \"$ACCESS_TEST_TOKEN\" >&2; exit 1", ""},
		{"invalid", "printf '%s' \"$ACCESS_TEST_TOKEN\"", "not-a-jwt"},
		{"expired", "printf '%s' \"$ACCESS_TEST_TOKEN\"", accessJWT(1)},
		{"oversized", "printf '%s' \"$ACCESS_TEST_TOKEN\"", accessJWT(time.Now().Add(time.Hour).Unix()) + strings.Repeat("a", accessTokenLimit)},
		{"missing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeAccessCommand(t, tc.script)
			if tc.token != "" {
				t.Setenv("ACCESS_TEST_TOKEN", tc.token)
			}
			if tc.name == "missing" {
				t.Setenv("PATH", t.TempDir())
			}
			var sent int
			opts := accessOptions(t, WithAccessSession(t.Context()), "api.example", roundTripFunc(func(*http.Request) (*http.Response, error) {
				sent++
				return nil, errors.New("unexpected request")
			}))
			_, err := DoRaw(t.Context(), "api.example", "POST", "/write", map[string]string{"value": "one"}, opts)
			var le *LatheError
			if tc.name == "oversized" && (!errors.As(err, &le) || le.Message != "cloudflared returned an oversized token") {
				t.Fatalf("oversized token was not rejected: error=%v requests=%d", err, sent)
			}
			if err == nil || sent != 0 {
				t.Fatalf("error=%v requests=%d", err, sent)
			}
			if !errors.As(err, &le) || !strings.Contains(le.Hint, "cloudflared access login --quiet https://api.example") {
				t.Fatalf("missing recovery command: %v", err)
			}
			var output bytes.Buffer
			FormatError(err, "json", &output)
			if strings.Contains(output.String(), os.Getenv("ACCESS_TEST_TOKEN")) {
				t.Fatal("token leaked in error")
			}
			if tc.name == "missing" && len(accessCalls(t, log)) != 0 {
				t.Fatal("missing binary executed")
			}
		})
	}
}

func TestAccessTokenCancellationAndTimeout(t *testing.T) {
	for _, deadline := range []time.Duration{30 * time.Millisecond, 0} {
		t.Run(deadline.String(), func(t *testing.T) {
			fakeAccessCommand(t, "exec /bin/sleep 60")
			ctx := t.Context()
			if deadline != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, deadline)
				defer cancel()
			}
			start := time.Now()
			_, err := readAccessToken(ctx, "https://api.example")
			if err == nil || time.Since(start) > accessTokenTimeout+2*time.Second {
				t.Fatalf("unbounded lookup: error=%v elapsed=%s", err, time.Since(start))
			}
			if deadline != 0 && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestAccessTokenWaitDelayBoundsInheritedStdout(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("ACCESS_TEST_CHILD_PID", pidPath)
	fakeAccessCommand(t, "/bin/sleep 60 &\nprintf '%s' \"$!\" > \"$ACCESS_TEST_CHILD_PID\"\nwait")
	killChild := func() {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			t.Error(err)
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Error(err)
			return
		}
		process, err := os.FindProcess(pid)
		if err == nil {
			_ = process.Kill()
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := readAccessToken(context.Background(), "https://api.example")
		done <- err
	}()
	timer := time.NewTimer(accessTokenTimeout + 2*time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		killChild()
		var le *LatheError
		if !errors.As(err, &le) || le.Message != "cloudflared token lookup timed out" {
			t.Fatalf("inherited stdout lost lookup timeout: %v", err)
		}
	case <-timer.C:
		killChild()
		select {
		case err := <-done:
			t.Fatalf("inherited stdout kept the lookup open past its timeout budget: %v", err)
		case <-time.After(time.Second):
		}
		t.Fatal("inherited stdout kept the lookup open past its timeout budget")
	}
}

func TestAccessCacheConcurrentAndPerOrigin(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	ctx := WithAccessSession(t.Context())
	var sent atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			opts := accessOptions(t, ctx, "api.example", transport)
			if _, err := DoRaw(ctx, "api.example", "GET", "/", nil, opts); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	opts := accessOptions(t, ctx, "other.example", transport)
	if _, err := DoRaw(ctx, "other.example", "GET", "/", nil, opts); err != nil {
		t.Fatal(err)
	}
	if sent.Load() != 21 || len(accessCalls(t, log)) != 2 {
		t.Fatalf("requests=%d calls=%v", sent.Load(), accessCalls(t, log))
	}
}

func TestAccessCachedExpiryAndUnconfiguredHost(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	ctx := WithAccessSession(t.Context())
	var sent int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	opts, err := HostClientOptions(ctx, "other.example", config.HostEntry{}, false)
	if err != nil {
		t.Fatal(err)
	}
	opts.Transport = transport
	if _, err := DoRaw(ctx, "other.example", "GET", "/", nil, opts); err != nil {
		t.Fatal(err)
	}
	if len(accessCalls(t, log)) != 0 {
		t.Fatal("unconfigured host executed cloudflared")
	}
	opts = accessOptions(t, ctx, "api.example", transport)
	if _, err := DoRaw(ctx, "api.example", "POST", "/write", nil, opts); err != nil {
		t.Fatal(err)
	}
	lookup := opts.access.session.lookups["https://api.example"]
	lookup.token.expires = 1
	if _, err := DoRaw(ctx, "api.example", "POST", "/write", nil, opts); err == nil {
		t.Fatal("cached expired token reused")
	}
	if sent != 2 || len(accessCalls(t, log)) != 1 {
		t.Fatalf("requests=%d lookups=%v", sent, accessCalls(t, log))
	}
}

func TestAccessExpiryDuringRetryStopsNextAttempt(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	ctx := WithAccessSession(t.Context())
	var sent int
	opts := accessOptions(t, ctx, "api.example", nil)
	opts.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent++
		opts.access.session.mu.Lock()
		opts.access.session.lookups["https://api.example"].token.expires = 1
		opts.access.session.mu.Unlock()
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	opts.MaxRetries = 1
	_, err := DoRaw(ctx, "api.example", "GET", "/", nil, opts)
	var le *LatheError
	if !errors.As(err, &le) || le.Message != "Cloudflare Access token expired" || sent != 1 || len(accessCalls(t, log)) != 1 {
		t.Fatalf("error=%v requests=%d calls=%v", err, sent, accessCalls(t, log))
	}
}

func TestAccessLookupCallerCancellation(t *testing.T) {
	for _, mode := range []string{"timeout-then-fresh", "cancel-with-waiter"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeAccessCommand(t, "while [ ! -f \"$ACCESS_TEST_RELEASE\" ]; do /bin/sleep 0.01; done; printf '%s' \"$ACCESS_TEST_TOKEN\"")
			release := filepath.Join(t.TempDir(), "release")
			t.Setenv("ACCESS_TEST_RELEASE", release)
			ctx, finish := context.WithTimeout(WithAccessSession(t.Context()), 5*time.Second)
			defer finish()
			opts := accessOptions(t, ctx, "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}))
			firstCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			firstOpts := opts
			if mode == "timeout-then-fresh" {
				firstOpts.Timeout = time.Second
			}
			first := make(chan error, 1)
			go func() { _, err := DoRaw(firstCtx, "api.example", "GET", "/first", nil, firstOpts); first <- err }()
			for len(accessCalls(t, log)) == 0 {
				select {
				case <-ctx.Done():
					t.Fatal("lookup did not start")
				case <-time.After(time.Millisecond):
				}
			}
			waiter := make(chan error, 1)
			if mode == "cancel-with-waiter" {
				go func() { _, err := DoRaw(ctx, "api.example", "GET", "/waiter", nil, opts); waiter <- err }()
				cancel()
			}
			firstErr := <-first
			if !errors.Is(firstErr, context.Canceled) && !errors.Is(firstErr, context.DeadlineExceeded) {
				t.Fatalf("first caller: %v", firstErr)
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "timeout-then-fresh" {
				_, err := DoRaw(ctx, "api.example", "GET", "/fresh", nil, opts)
				waiter <- err
			}
			if err := <-waiter; err != nil {
				t.Fatalf("live caller inherited another caller's context error: %v", err)
			}
			if len(accessCalls(t, log)) != 1 {
				t.Fatalf("shared lookup executions: %v", accessCalls(t, log))
			}
		})
	}
}
