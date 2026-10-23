package runtime

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/spf13/cobra"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accessOptions(t *testing.T, ctx context.Context, hostname string, transport http.RoundTripper) ClientOptions {
	t.Helper()
	opts, err := HostClientOptions(ctx, hostname, config.HostEntry{CloudflareAccess: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	opts.Transport = transport
	opts.MaxRetries = -1
	return opts
}

func TestAccessOriginAndTLSRestrictions(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	for _, hostname := range []string{"http://api.example", "https://u:p@api.example", "api.example/path", "api.example///", "api.example:", "-api.example", "api..example", "api.example?x=y", "api.example#frag", "api.example:0", "api.example:65536", "api.example:abc", "api.$example"} {
		if _, err := HostClientOptions(t.Context(), hostname, config.HostEntry{CloudflareAccess: true}, false); err == nil {
			t.Errorf("accepted %q", hostname)
		}
	}
	for _, entry := range []config.HostEntry{{CloudflareAccess: true}, {CloudflareAccess: true, Insecure: true}} {
		if _, err := HostClientOptions(t.Context(), "api.example", entry, true); err == nil {
			t.Fatal("Access accepted insecure TLS")
		}
	}
	ctx := WithAccessSession(t.Context())
	var requests int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("cf-access-token") == "" {
			t.Error("no Access token")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	opts := accessOptions(t, ctx, "https://API.example:443", transport)
	for _, target := range []string{"https://api.example", "https://api.example:443"} {
		if _, err := DoRaw(ctx, target, "GET", "/", nil, opts); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"http://api.example", "https://sub.api.example", "https://other.example", "https://api.example:444"} {
		if _, err := DoRaw(ctx, target, "GET", "/", nil, opts); err == nil {
			t.Errorf("accepted %s", target)
		}
	}
	if requests != 2 || len(accessCalls(t, log)) != 1 || accessCalls(t, log)[0] != "access token -app=https://api.example" {
		t.Fatalf("requests=%d calls=%v", requests, accessCalls(t, log))
	}
}

func TestAccessRuntimePaths(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("cf-access-token") != os.Getenv("ACCESS_TEST_TOKEN") || r.Header.Get("Authorization") != "Bearer api-token" {
			t.Error("missing Access or API credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pages":
			if r.URL.Query().Get("page") == "2" {
				_, _ = io.WriteString(w, `{"items":[]}`)
			} else {
				_, _ = io.WriteString(w, `{"items":[{"id":1}],"next_page":"2"}`)
			}
		case "/schema":
			_, _ = io.WriteString(w, `{"schema":{"type":"object"}}`)
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: done\ndata: {\"answer\":\"ok\"}\n\n")
		case "/start":
			w.Header().Set("Location", "/poll")
			w.WriteHeader(202)
		case "/binary":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0, 1, 255})
		default:
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer srv.Close()
	ctx := WithAccessSession(t.Context())
	opts := accessOptions(t, ctx, srv.URL, srv.Client().Transport)
	opts.Auth = BearerAuth{Token: "api-token"}
	if _, err := DoRaw(ctx, srv.URL, "GET", "/api", nil, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := PaginateAll(ctx, srv.URL, "GET", "/pages", nil, opts, PaginationHint{Strategy: "cursor", TokenParam: "page", TokenField: "next_page"}, "items", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := InvokeOperation(ctx, CommandSpec{Method: "POST", PathTpl: "/start"}, OperationInput{}, OperationOptions{Hostname: srv.URL, Client: opts, Wait: true}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/raw", "/binary", "/stream"} {
		var out bytes.Buffer
		copyOpts := opts
		if path == "/binary" {
			copyOpts.Accept = "application/octet-stream"
			copyOpts.binaryContentGuard = true
		}
		if _, err := doRawFull(ctx, srv.URL, "GET", path, nil, copyOpts, &out); err != nil || out.Len() == 0 {
			t.Fatalf("%s: %v", path, err)
		}
	}
	schemaTarget := CommandSpec{Method: "POST", PathTpl: "/schema-target", RequestBody: &RequestBody{MediaType: "application/json", RuntimeSchema: &RuntimeSchemaSpec{
		Operation: CommandSpec{Method: "GET", PathTpl: "/schema"}, ResponsePath: "schema",
	}}}
	if _, err := InvokeOperation(ctx, schemaTarget, OperationInput{FileBody: []byte(`{}`), HasFile: true}, OperationOptions{Hostname: srv.URL, Client: opts}); err != nil {
		t.Fatal(err)
	}
	streamTarget := CommandSpec{Method: "GET", PathTpl: "/events", Output: OutputHints{Streaming: &StreamingHint{Strategy: "sse", Policy: &StreamPolicy{
		DataFormat: "json", Collect: &StreamCollectHint{RequireStop: true, StopEvents: []string{"done"}, Fields: []StreamFieldRule{{Events: []string{"done"}, From: "answer", To: "answer", Reduce: "last"}}},
	}}}}
	if result, err := InvokeOperation(ctx, streamTarget, OperationInput{}, OperationOptions{Hostname: srv.URL, Client: opts}); err != nil || !bytes.Contains(result.Data, []byte("ok")) {
		t.Fatalf("stream collect: %v data=%s", err, result.Data)
	}
	if len(accessCalls(t, log)) != 1 || !strings.Contains(strings.Join(paths, ","), "/poll") {
		t.Fatalf("calls=%v paths=%v", accessCalls(t, log), paths)
	}
}

func TestAccessDryRunNoExecAndErrorRedaction(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	ctx := WithAccessSession(t.Context())
	opts := accessOptions(t, ctx, "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"message":%q}`, r.Header.Get("cf-access-token"))))}, nil
	}))
	result, err := InvokeOperation(ctx, CommandSpec{Method: "POST", PathTpl: "/write"}, OperationInput{}, OperationOptions{Hostname: "api.example", Client: opts, DryRun: true})
	if err != nil || result.DryRun == nil || len(accessCalls(t, log)) != 0 {
		t.Fatalf("dry-run: error=%v calls=%v", err, accessCalls(t, log))
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatal(err)
	}
	_, err = DoRaw(ctx, "api.example", "GET", "/", nil, opts)
	var output bytes.Buffer
	FormatError(err, "json", &output)
	if strings.Contains(output.String(), os.Getenv("ACCESS_TEST_TOKEN")) {
		t.Fatal("token leaked from API error body")
	}
}

func TestAccessRejectedTokenDoesNotReplayOrRefreshAPIAuth(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	var sent, refreshes int
	opts := accessOptions(t, WithAccessSession(t.Context()), "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://team.cloudflareaccess.com/cdn-cgi/access/login"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}))
	opts.RefreshAuth = func(context.Context) (Authenticator, error) {
		refreshes++
		return BearerAuth{Token: "new"}, nil
	}
	_, err := DoRaw(t.Context(), "api.example", "POST", "/write", map[string]int{"x": 1}, opts)
	var le *LatheError
	if !errors.As(err, &le) || !strings.Contains(le.Hint, "cloudflared access login --quiet https://api.example") || sent != 1 || refreshes != 0 || len(accessCalls(t, log)) != 1 {
		t.Fatalf("error=%v requests=%d refreshes=%d calls=%v", err, sent, refreshes, accessCalls(t, log))
	}
}

func TestAccessPreservesRetryTimeoutAndDebug(t *testing.T) {
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	ctx := WithAccessSession(t.Context())
	var sent int
	opts := accessOptions(t, ctx, "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent++
		status := 200
		if sent == 1 {
			status = 503
		}
		if r.Header.Get("cf-access-token") == "" {
			t.Error("retry lost Access token")
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}))
	opts.MaxRetries = 1
	opts.Debug = true
	opts.Timeout = 3 * time.Second
	capture, err := os.CreateTemp(t.TempDir(), "debug")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = capture
	defer func() { os.Stderr = original; _ = capture.Close() }()
	if _, err := DoRaw(ctx, "api.example", "GET", "/", nil, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 || len(accessCalls(t, log)) != 1 || strings.Contains(string(data), os.Getenv("ACCESS_TEST_TOKEN")) {
		t.Fatalf("requests=%d calls=%v debug leaked=%v", sent, accessCalls(t, log), strings.Contains(string(data), os.Getenv("ACCESS_TEST_TOKEN")))
	}
	if HTTPClient(opts).Timeout != 3*time.Second || httpClient(ClientOptions{access: opts.access}, true).Timeout != 0 {
		t.Fatal("timeout or streaming semantics changed")
	}
}

func TestAccessHostWorkflowAndRefresh(t *testing.T) {
	if os.Getenv("ACCESS_RUNTIME_CHILD") == "1" {
		accessHostWorkflowChild(t)
		return
	}
	log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
	var refreshes int
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("cf-access-token") != os.Getenv("ACCESS_TEST_TOKEN") {
			t.Error("host workflow or refresh lost Access token")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/refresh" {
			refreshes++
			token := "api-proactive"
			if refreshes == 2 {
				token = "api-reactive"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "refresh_token": "api-refresh", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") == "Bearer api-proactive" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer api-reactive" {
			t.Error("API auth was replaced by Access")
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	certPath := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), exe, "-test.run=^TestAccessHostWorkflowAndRefresh$", "-test.v")
	child.Env = append(os.Environ(), "ACCESS_RUNTIME_CHILD=1", "ACCESS_RUNTIME_ORIGIN="+srv.URL, "ACCESS_RUNTIME_ROOT="+certPath, "GODEBUG=x509usefallbackroots=1", "ACCESS_RUNTIME_CONFIG="+t.TempDir())
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if refreshes != 2 || len(paths) != 6 || len(accessCalls(t, log)) != 1 {
		t.Fatalf("refreshes=%d requests=%v cloudflared=%v", refreshes, paths, accessCalls(t, log))
	}
}

func accessHostWorkflowChild(t *testing.T) {
	t.Helper()
	pool := x509.NewCertPool()
	pemData, err := os.ReadFile(os.Getenv("ACCESS_RUNTIME_ROOT"))
	if err != nil || !pool.AppendCertsFromPEM(pemData) {
		t.Fatalf("load test root: %v", err)
	}
	x509.SetFallbackRoots(pool)
	origin := os.Getenv("ACCESS_RUNTIME_ORIGIN")
	config.Bind(&config.Manifest{
		CLI:  config.CLIInfo{Name: "access-test", ConfigDirEnv: "ACCESS_RUNTIME_CONFIG", HostEnv: "ACCESS_RUNTIME_HOST"},
		Auth: config.AuthInfo{Login: &config.AuthLogin{Type: config.AuthLoginOAuthDevice, RefreshPath: "/refresh"}},
	})
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	hosts.Set(origin, config.HostEntry{CloudflareAccess: true, AuthType: "bearer", OAuthToken: "api-old", OAuthRefreshToken: "api-refresh", OAuthExpiresAt: 1})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "access-test"}
	root.SetContext(WithAccessSession(t.Context()))
	root.PersistentFlags().String("hostname", origin, "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().String("output", "json", "")
	host, opts, err := LoadHostOptions(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DoRaw(root.Context(), host, "GET", "/api", nil, opts); err != nil {
		t.Fatal(err)
	}
	if err := BuildWorkflows(root, []WorkflowSpec{{Use: "work", Steps: []WorkflowStepSpec{
		{ID: "one", Operation: CommandSpec{Method: "GET", PathTpl: "/step-one"}},
		{ID: "two", Operation: CommandSpec{Method: "GET", PathTpl: "/step-two"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	root.SetOut(io.Discard)
	root.SetArgs([]string{"work"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
