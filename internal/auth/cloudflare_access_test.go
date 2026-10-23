package auth

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"github.com/spf13/cobra"
)

func TestCloudflareAccessLoginPreservesAndDisables(t *testing.T) {
	var mu sync.Mutex
	var credentials []string
	token := accessTestToken(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/validate" {
			http.NotFound(w, r)
			return
		}
		credential := r.Header.Get("Authorization")
		wantAccess := token
		if credential == "Bearer disabled-api" {
			wantAccess = ""
		}
		if r.Header.Get("Cf-Access-Token") != wantAccess {
			t.Error("validation request did not preserve the Access choice")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		credentials = append(credentials, credential)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"user":"owner"}`)
	}))
	defer srv.Close()
	fixture := newAccessLoginFixture(t, srv, token)
	fixture.run("first-api", "auth", "login", "--with-token", "--cloudflare-access")
	fixture.requireEntry(true, "first-api")
	fixture.requireCalls(1)
	fixture.requireStatus(true)
	fixture.requireCalls(1)
	fixture.run("second-api", "auth", "login", "--with-token")
	fixture.requireEntry(true, "second-api")
	fixture.requireCalls(2)
	fixture.run("disabled-api", "auth", "login", "--with-token", "--cloudflare-access=false")
	fixture.requireEntry(false, "disabled-api")
	fixture.requireStatus(false)
	fixture.requireCalls(2)
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"Bearer first-api", "Bearer second-api", "Bearer disabled-api"}; !reflect.DeepEqual(credentials, want) {
		t.Fatalf("API credentials = %q, want %q", credentials, want)
	}
}

func TestCloudflareAccessExplicitDisableDoesNotAcquireToken(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cf-Access-Token") != "" || r.Header.Get("Authorization") != "Bearer disabled-api" {
			t.Error("disabled login changed API authentication or sent an Access token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"user":"owner"}`)
	}))
	defer srv.Close()
	fixture := newAccessLoginFixture(t, srv, accessTestToken(t))
	config.Bind(accessLoginManifest())
	t.Setenv("DEMO_CONFIG_DIR", fixture.dir)
	hosts, err := config.LoadHosts()
	testutil.NoError(t, err)
	hosts.Set(srv.URL, config.HostEntry{AuthType: "bearer", OAuthToken: "old-api", CloudflareAccess: true})
	testutil.NoError(t, hosts.Save())
	fixture.run("disabled-api", "auth", "login", "--with-token", "--cloudflare-access=false")
	fixture.requireEntry(false, "disabled-api")
	fixture.requireStatus(false)
	fixture.requireCalls(0)
}

func TestCloudflareAccessDeviceLoginSharesSession(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	token := accessTestToken(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cf-Access-Token") != token {
			t.Error("device login request omitted the Access token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		wantAuth := ""
		if r.URL.Path == "/validate" {
			wantAuth = "Bearer device-api"
		}
		if r.Header.Get("Authorization") != wantAuth {
			t.Error("device login changed API authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/start":
			_, _ = io.WriteString(w, `{"device_code":"device-1","verification_uri":"https://example.com/device","expires_in":60}`)
		case "/token":
			_, _ = io.WriteString(w, `{"access_token":"device-api","refresh_token":"device-refresh"}`)
		case "/validate":
			_, _ = io.WriteString(w, `{"user":"owner"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	fixture := newAccessLoginFixture(t, srv, token)
	fixture.run("", "auth", "login", "--device-auth", "--no-browser", "--cloudflare-access")
	fixture.requireEntry(true, "device-api")
	fixture.requireCalls(1)
	fixture.requireStatus(true)
	fixture.requireCalls(1)
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"/start", "/token", "/validate"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("request paths = %q, want %q", paths, want)
	}
}

func TestCloudflareAccessStatusDoesNotAcquireToken(t *testing.T) {
	m := accessLoginManifest()
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	hosts, err := config.LoadHosts()
	testutil.NoError(t, err)
	hosts.Set("api.example.com", config.HostEntry{AuthType: "bearer", OAuthToken: "api-token", CloudflareAccess: true})
	testutil.NoError(t, hosts.Save())
	for _, format := range []string{"json", "table"} {
		root := newAuthRoot(m, "api.example.com")
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{"auth", "status", "--output", format})
		testutil.NoError(t, root.Execute())
		if format == "json" {
			var status authStatus
			testutil.NoError(t, json.Unmarshal(out.Bytes(), &status))
			testutil.Require(t, len(status.Hosts) == 1 && status.Hosts[0].CloudflareAccess, "Access status missing")
		} else if !strings.Contains(out.String(), "Cloudflare Access: enabled (token not checked)") {
			t.Fatalf("text status = %q", out.String())
		}
	}
}

func TestCloudflareAccessPublicCLIRejectsBeforeSecret(t *testing.T) {
	m := accessLoginManifest()
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	root := newAuthRoot(m, "api.example.com")
	public := &cobra.Command{Use: "public"}
	runtime.AttachCatalogCommand(public, "demo", runtime.CommandSpec{Method: "GET", PathTpl: "/public", Security: &runtime.SecurityHint{Public: true}})
	root.AddCommand(public)
	input, err := os.CreateTemp(t.TempDir(), "stdin")
	testutil.NoError(t, err)
	defer func() { _ = input.Close() }()
	_, err = input.WriteString("unread-api-secret")
	testutil.NoError(t, err)
	_, err = input.Seek(0, io.SeekStart)
	testutil.NoError(t, err)
	oldStdin := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = oldStdin }()
	root.SetArgs([]string{"auth", "login", "--with-token", "--cloudflare-access"})
	err = root.Execute()
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "API authentication"), "public CLI error = %v", err)
	testutil.Require(t, runtime.ClassifyError(err).Code == runtime.CodeUsage, "public CLI error must be usage: %v", err)
	remaining, err := io.ReadAll(input)
	testutil.NoError(t, err)
	testutil.Require(t, string(remaining) == "unread-api-secret", "login read the API secret before rejecting Access")
}

func TestCloudflareAccessLoginTimeoutPreservesHosts(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer srv.Close()
	fixture := newAccessLoginFixture(t, srv, accessTestToken(t))
	for _, setting := range fixture.env {
		if path, ok := strings.CutPrefix(setting, "PATH="); ok {
			testutil.NoError(t, os.WriteFile(filepath.Join(filepath.SplitList(path)[0], "cloudflared"), []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0o700))
		}
	}
	config.Bind(accessLoginManifest())
	t.Setenv("DEMO_CONFIG_DIR", fixture.dir)
	hosts, err := config.LoadHosts()
	testutil.NoError(t, err)
	hosts.Set(fixture.host, config.HostEntry{AuthType: "bearer", OAuthToken: "old-api", Selected: true})
	testutil.NoError(t, hosts.Save())
	hostsPath := filepath.Join(fixture.dir, "hosts.yml")
	before, err := os.ReadFile(hostsPath)
	testutil.NoError(t, err)
	args, err := json.Marshal([]string{"auth", "login", "--with-token", "--cloudflare-access"})
	testutil.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAccessLoginProcess$")
	cmd.Env = append(append(os.Environ(), fixture.env...), "LATHE_AUTH_ACCESS_ARGS="+string(args))
	cmd.Stdin = strings.NewReader("replacement-api")
	output, err := cmd.CombinedOutput()
	testutil.Require(t, err != nil, "hung cloudflared login succeeded")
	after, err := os.ReadFile(hostsPath)
	testutil.NoError(t, err)
	testutil.Require(t, bytes.Equal(before, after), "failed login changed hosts.yml")
	testutil.Require(t, requests.Load() == 0, "failed lookup sent an API request")
	testutil.Require(t, strings.Contains(string(output), "cloudflared token lookup timed out"), "login lost the lookup timeout: %s", output)
}

func TestAccessLoginProcess(t *testing.T) {
	if os.Getenv("LATHE_AUTH_ACCESS_HELPER") != "1" {
		return
	}
	cert, err := os.ReadFile(os.Getenv("SSL_CERT_FILE"))
	testutil.NoError(t, err)
	roots := x509.NewCertPool()
	testutil.Require(t, roots.AppendCertsFromPEM(cert), "test root certificate is invalid")
	x509.SetFallbackRoots(roots)
	m := accessLoginManifest()
	config.Bind(m)
	var args []string
	testutil.NoError(t, json.Unmarshal([]byte(os.Getenv("LATHE_AUTH_ACCESS_ARGS")), &args))
	root := newAuthRoot(m, os.Getenv("LATHE_AUTH_ACCESS_HOST"))
	api := &cobra.Command{Use: "api"}
	runtime.AttachCatalogCommand(api, "demo", runtime.CommandSpec{Method: "GET", PathTpl: "/api"})
	root.AddCommand(api)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.SetArgs(args)
	testutil.NoError(t, root.Execute())
}

func accessLoginManifest() *config.Manifest {
	return &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Auth: config.AuthInfo{
			Login:    &config.AuthLogin{Type: config.AuthLoginOAuthDevice, StartPath: "/start", TokenPath: "/token"},
			Validate: &config.AuthValidate{Path: "/validate", Display: config.AuthValidateDisplay{UsernameField: "user"}},
		},
	}
}

func accessTestToken(t *testing.T) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "aud": []string{"test-audience"}})
	testutil.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".test-signature"
}

type accessLoginFixture struct {
	t     *testing.T
	host  string
	dir   string
	env   []string
	token string
	calls string
}

func newAccessLoginFixture(t *testing.T, srv *httptest.Server, token string) *accessLoginFixture {
	t.Helper()
	dir := t.TempDir()
	bin := t.TempDir()
	certPath := filepath.Join(t.TempDir(), "root.pem")
	testutil.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600))
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$LATHE_AUTH_ACCESS_CALLS\"\nprintf '%s\\n' \"$LATHE_AUTH_ACCESS_TOKEN\"\n"
	testutil.NoError(t, os.WriteFile(filepath.Join(bin, "cloudflared"), []byte(script), 0o700))
	return &accessLoginFixture{t: t, host: srv.URL, dir: dir, token: token, calls: calls, env: []string{
		"LATHE_AUTH_ACCESS_HELPER=1", "LATHE_AUTH_ACCESS_HOST=" + srv.URL, "DEMO_CONFIG_DIR=" + dir,
		"LATHE_AUTH_ACCESS_TOKEN=" + token, "LATHE_AUTH_ACCESS_CALLS=" + calls,
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "SSL_CERT_FILE=" + certPath,
		"GODEBUG=x509usefallbackroots=1",
	}}
}

func (f *accessLoginFixture) run(secret string, args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAccessLoginProcess$")
	encoded, err := json.Marshal(args)
	testutil.NoError(f.t, err)
	cmd.Env = append(append(os.Environ(), f.env...), "LATHE_AUTH_ACCESS_ARGS="+string(encoded))
	cmd.Stdin = strings.NewReader(secret)
	output, err := cmd.CombinedOutput()
	if bytes.Contains(output, []byte(f.token)) {
		f.t.Fatal("Access token leaked into command output")
	}
	if err != nil {
		f.t.Fatalf("auth command failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, "hosts.yml"))
	testutil.NoError(f.t, err)
	if bytes.Contains(data, []byte(f.token)) {
		f.t.Fatal("Access token persisted in hosts.yml")
	}
	return string(output)
}

func (f *accessLoginFixture) requireEntry(access bool, apiToken string) {
	f.t.Helper()
	config.Bind(accessLoginManifest())
	f.t.Setenv("DEMO_CONFIG_DIR", f.dir)
	hosts, err := config.LoadHosts()
	testutil.NoError(f.t, err)
	entry, ok := hosts.Get(f.host)
	testutil.Require(f.t, ok && entry.CloudflareAccess == access && entry.OAuthToken == apiToken && entry.User == "owner" && !entry.Insecure, "saved host does not match the login choice")
}

func (f *accessLoginFixture) requireCalls(want int) {
	f.t.Helper()
	data, err := os.ReadFile(f.calls)
	if os.IsNotExist(err) && want == 0 {
		return
	}
	testutil.NoError(f.t, err)
	if got := len(strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' })); got != want {
		f.t.Fatalf("cloudflared calls = %d, want %d", got, want)
	}
}

func (f *accessLoginFixture) requireStatus(access bool) {
	f.t.Helper()
	jsonOut := f.run("", "auth", "status", "--output", "json")
	want := fmt.Sprintf(`"cloudflare_access": %t`, access)
	if access && !strings.Contains(jsonOut, want) {
		f.t.Fatal("JSON status omitted the Access setting")
	}
	if !access && strings.Contains(jsonOut, "cloudflare_access") {
		f.t.Fatal("JSON status included the disabled Access setting")
	}
	textOut := f.run("", "auth", "status")
	if strings.Contains(textOut, "enabled (token not checked)") != access {
		f.t.Fatal("text status does not match the Access setting")
	}
}
