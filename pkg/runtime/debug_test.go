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
)

func captureStderr(t *testing.T) *os.File {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
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
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	if !strings.Contains(out, "> POST request") || !strings.Contains(out, "< HTTP 400") {
		t.Fatalf("debug output missing safe metadata:\n%s", out)
	}
	for _, leaked := range []string{"private-path", "query-secret", "request-header-secret", "request-body-secret", "response-header-secret", "response-body-secret"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("debug output leaked %q:\n%s", leaked, out)
		}
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
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	readStderr(t, r)

	if string(body) != `{"data":1}` {
		t.Errorf("response body = %q, want %q", string(body), `{"data":1}`)
	}
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
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = resp.Body.Close()
	out := readStderr(t, stderr)
	if string(body) != "data: first\n\ndata: second\n\n" {
		t.Fatalf("response body = %q", body)
	}
	if strings.Contains(out, "[body") {
		t.Fatalf("debug output unexpectedly dumped streaming body:\n%s", out)
	}
}

func TestDebugTransport_LogsResolvedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := captureStderr(t)
	dt := &debugTransport{inner: http.DefaultTransport, hostname: "staging.example.com"}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL, nil)
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	if !strings.Contains(out, "> POST request host=staging.example.com") {
		t.Fatalf("debug dump missing resolved host:\n%s", out)
	}
}
