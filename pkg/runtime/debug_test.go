package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func captureStderr(t *testing.T) *os.File {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	testutil.Require(t, err == nil, "os.Pipe: %v", err)
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = original
		_ = w.Close()
		_ = r.Close()
	})
	return r
}

func readStderr(t *testing.T, r *os.File) string {
	t.Helper()
	os.Stderr.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r) //nolint:errcheck
	return buf.String()
}

func TestDebugTransport_LogsOnlySafeMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Secret", "response-header-secret")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"response-body-secret"}`))
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/private-path?token=query-secret", strings.NewReader(`{"value":"request-body-secret"}`))
	req.Header.Set("Authorization", "Bearer request-header-secret")
	resp, err := dt.RoundTrip(req)
	testutil.Require(t, err == nil, "RoundTrip: %v", err)
	resp.Body.Close()

	out := readStderr(t, r)
	testutil.Require(t, strings.Contains(out, "> POST request") && strings.Contains(out, "< HTTP 400"), "debug output missing safe metadata:\n%s", out)
	for _, leaked := range []string{"private-path", "query-secret", "request-header-secret", "request-body-secret", "response-header-secret", "response-body-secret"} {
		testutil.Require(t, !strings.Contains(out, leaked), "debug output leaked %q:\n%s", leaked, out)
	}
}

func TestDebugTransport_PreservesResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":1}`))
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := dt.RoundTrip(req)
	testutil.Require(t, err == nil, "RoundTrip: %v", err)

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	readStderr(t, r)

	testutil.Check(t, string(body) == `{"data":1}`, "response body = %q, want %q", string(body), `{"data":1}`)
}

func TestDebugTransport_DoesNotPeekStreamingResponse(t *testing.T) {
	firstSent := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(firstSent)
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: second\n\n")
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	stderr := captureStderr(t)
	dt := &debugTransport{inner: http.DefaultTransport, streaming: true}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := dt.RoundTrip(req)
		respCh <- resp
		errCh <- err
	}()
	select {
	case <-firstSent:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("server did not send first event")
	}

	var resp *http.Response
	select {
	case resp = <-respCh:
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-respCh
		t.Fatal("debug transport waited for the streaming response to close")
	}
	if err := <-errCh; err != nil {
		close(release)
		t.Fatalf("RoundTrip: %v", err)
	}
	close(release)
	body, err := io.ReadAll(resp.Body)
	testutil.Require(t, err == nil, "read response: %v", err)
	_ = resp.Body.Close()
	out := readStderr(t, stderr)
	testutil.Require(t, string(body) == "data: first\n\ndata: second\n\n", "response body = %q", body)
	testutil.Require(t, !strings.Contains(out, "[body"), "debug output unexpectedly dumped streaming body:\n%s", out)
}
