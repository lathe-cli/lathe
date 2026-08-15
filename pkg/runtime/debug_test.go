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

func TestDebugTransport_LogsJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	body := strings.NewReader(`{"name":"test"}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/api", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	if !strings.Contains(out, `{"name":"test"}`) {
		t.Errorf("stderr missing request body:\n%s", out)
	}
	if !strings.Contains(out, `{"result":"ok"}`) {
		t.Errorf("stderr missing response body:\n%s", out)
	}
	if !strings.Contains(out, "[body") {
		t.Errorf("stderr missing body size label:\n%s", out)
	}
}

func TestDebugTransport_RedactsSensitiveHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=response-secret")
		w.Header().Set("X-Api-Key", "response-key")
		w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer request-token")
	req.Header.Set("Cookie", "session=request-secret")
	req.Header.Set("X-API-Key", "request-key")
	req.Header.Set("X-Trace-Token", "trace-secret")
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	for _, leaked := range []string{"request-token", "request-secret", "request-key", "trace-secret", "response-secret", "response-key"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("debug output leaked %q:\n%s", leaked, out)
		}
	}
	if strings.Count(out, "***") < 6 {
		t.Fatalf("debug output did not redact expected headers:\n%s", out)
	}
}

func TestDebugTransport_RedactsQueryCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/jobs?opaque=response-query-secret")
		w.Header().Set("Content-Location", "https://user:response-userinfo-secret%zz@example.com/jobs?sig=response-malformed-secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := captureStderr(t)
	dt := &debugTransport{inner: http.DefaultTransport, sensitiveQueryParams: map[string]bool{"opaque": true}}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/api?key=debug-key-secret&authorization=debug-authorization-secret&X-Amz-Signature=debug-signature-secret&name=alice", nil)
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	for _, leaked := range []string{"debug-key-secret", "debug-authorization-secret", "debug-signature-secret", "response-query-secret", "response-malformed-secret", "response-userinfo-secret"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("debug output leaked %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "name=alice") {
		t.Fatalf("debug output lost non-sensitive query:\n%s", out)
	}
}

func TestDebugTransport_RedactsSensitiveJSONBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok","message":"token=response-message-secret; Bearer response-bearer","token":"response-token","nested":{"apiKey":"response-key"}}`))
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	body := strings.NewReader(`{"name":"test","message":"token=request-message-secret; bearer request-bearer","secret":"request-secret","nested":{"password":"request-password"}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/api", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	for _, leaked := range []string{"request-secret", "request-password", "request-message-secret", "request-bearer", "response-token", "response-key", "response-message-secret", "response-bearer"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("debug body leaked %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, `"name":"test"`) || !strings.Contains(out, `"result":"ok"`) {
		t.Fatalf("debug output lost non-sensitive fields:\n%s", out)
	}
	if strings.Count(out, "***") < 4 {
		t.Fatalf("debug output did not redact expected body fields:\n%s", out)
	}
}

func TestDebugTransport_TruncatesLargeBody(t *testing.T) {
	large := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(large))
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	if !strings.Contains(out, "showing first 4096") {
		t.Errorf("stderr missing truncation label:\n%s", out)
	}
}

func TestDebugTransport_SkipsBinaryBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer srv.Close()

	r := captureStderr(t)

	dt := &debugTransport{inner: http.DefaultTransport}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := dt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	out := readStderr(t, r)
	if strings.Contains(out, "[body") {
		t.Errorf("stderr should not contain body dump for binary content:\n%s", out)
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

func TestIsTextContent(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/xml", true},
		{"text/plain", true},
		{"text/html", true},
		{"application/x-www-form-urlencoded", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"", false},
		{"application/json; charset=utf-8", true},
	}
	for _, tc := range tests {
		if got := isTextContent(tc.ct); got != tc.want {
			t.Errorf("isTextContent(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
