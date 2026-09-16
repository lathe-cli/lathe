package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestOAuthDeviceLoginSavesBearerHost(t *testing.T) {
	var startCalled bool
	var tokenCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			startCalled = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode start body: %v", err)
			}
			testutil.Check(t, body["provider"] == "github", "provider = %q, want github", body["provider"])
			testutil.Check(t, body["hostname"] != "", "hostname missing")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "device-1",
				"user_code":                 "ABCD",
				"verification_uri_complete": "https://example.com/device?code=ABCD",
				"expires_in":                60,
			})
		case "/token":
			tokenCalled = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode token body: %v", err)
			}
			testutil.Check(t, body["device_code"] == "device-1", "device_code = %q, want device-1", body["device_code"])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"expires_in":    3600,
				"user": map[string]string{
					"email": "octo@example.com",
				},
			})
		case "/validate":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Auth: config.AuthInfo{Login: &config.AuthLogin{
			Type:      config.AuthLoginOAuthDevice,
			StartPath: "/start",
			TokenPath: "/token",
		}, Validate: &config.AuthValidate{Method: "GET", Path: "/validate"}},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	root := newAuthRoot(m, srv.URL)
	root.SetArgs([]string{"auth", "login", "--auth-type", "oauth", "--provider", "github"})

	testutil.NoError(t, root.Execute())
	testutil.Require(t, startCalled && tokenCalled, "startCalled=%v tokenCalled=%v", startCalled, tokenCalled)
	hosts, err := config.LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
	entry, ok := hosts.Get(srv.URL)
	testutil.Require(t, ok, "host not saved")
	testutil.Require(t, entry.AuthType == "bearer" && entry.LoginType == config.AuthLoginOAuthDevice && entry.LoginProvider == "github" && entry.OAuthToken == "access-1" && entry.OAuthRefreshToken == "refresh-1" && entry.User == "octo@example.com" && entry.OAuthExpiresAt != 0, "entry = %+v", entry)
}

func TestOAuthDeviceLoginRequiresAuthLoginConfig(t *testing.T) {
	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	root := newAuthRoot(m, "api.example.com")
	root.SetArgs([]string{"auth", "login", "--auth-type", "oauth"})

	err := root.Execute()
	var le *runtime.LatheError
	testutil.Require(t, errors.As(err, &le), "error = %T %v, want *runtime.LatheError", err, err)
	testutil.Require(t, le.Code == runtime.CodeUsage && le.ExitCode == runtime.ExitUsage, "code = %q exit = %d, want %q/%d", le.Code, le.ExitCode, runtime.CodeUsage, runtime.ExitUsage)
	var buf strings.Builder
	runtime.FormatError(err, "", &buf)
	if !strings.Contains(buf.String(), "oauth login is not configured for this CLI") || !strings.Contains(buf.String(), "auth.login") {
		t.Fatalf("formatted error = %q", buf.String())
	}
}

func TestOAuthDeviceLoginUsesManifestWireMapping(t *testing.T) {
	var startBody, pollBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			if err := json.NewDecoder(r.Body).Decode(&startBody); err != nil {
				t.Errorf("decode start body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-1",
				"user_code":        "ABCD",
				"verification_uri": "https://example.com/device",
				"expires_in":       60,
			})
		case "/token":
			if err := json.NewDecoder(r.Body).Decode(&pollBody); err != nil {
				t.Errorf("decode poll body: %v", err)
			}
			if err := config.MutateHosts(r.Context(), func(hosts *config.Hosts) error {
				entry, _ := hosts.Get("http://" + r.Host)
				entry.Contexts["organization"] = "org-concurrent"
				entry.Contexts["workspace"] = "ws-concurrent"
				hosts.Set("http://"+r.Host, entry)
				return nil
			}); err != nil {
				t.Errorf("update context during login: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":   "access-1",
				"account": map[string]string{"workspace_id": "ws-1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := &config.Manifest{
		CLI:      config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Contexts: map[string]config.ContextInfo{"organization": {}, "workspace": {}},
		Auth: config.AuthInfo{Login: &config.AuthLogin{
			Type:         config.AuthLoginOAuthDevice,
			StartPath:    "/start",
			TokenPath:    "/token",
			StartRequest: map[string]string{"client_id": "demo-cli", "device_label": "${device_label}"},
			PollRequest:  map[string]string{"client_id": "demo-cli", "device_code": "${device_code}"},
			PollResponse: config.AuthLoginPollResponse{AccessToken: "token", Contexts: map[string]string{"workspace": "account.workspace_id"}},
		}},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	testutil.Require(t, err == nil, "%v", err)
	hosts.Set(srv.URL, config.HostEntry{AuthType: "bearer", OAuthToken: "old", Contexts: map[string]string{"organization": "org-old", "workspace": "ws-old"}})
	testutil.NoError(t, hosts.Save())

	root := newAuthRoot(m, srv.URL)
	root.SetArgs([]string{"auth", "login", "--device-auth", "--no-browser", "--skip-validate"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, startBody["client_id"] == "demo-cli" && strings.HasPrefix(startBody["device_label"], "demo on ") && len(startBody) == 2, "start body = %#v", startBody)
	testutil.Require(t, pollBody["client_id"] == "demo-cli" && pollBody["device_code"] == "device-1" && len(pollBody) == 2, "poll body = %#v", pollBody)
	hosts, err = config.LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
	entry, ok := hosts.Get(srv.URL)
	testutil.Require(t, ok && entry.OAuthToken == "access-1" && entry.Contexts["workspace"] == "ws-1" && entry.Contexts["organization"] == "org-concurrent", "entry = %+v, found = %v", entry, ok)
}

func TestContextCommandsRespectLocalPolicyAndEnvironmentPrecedence(t *testing.T) {
	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Contexts: map[string]config.ContextInfo{
			"organization": {Env: "DEMO_ORG_ID", LocalSet: true},
			"workspace":    {},
		},
	}
	managedOnly := newContextCommand(&config.Manifest{Contexts: map[string]config.ContextInfo{"workspace": {}}})
	for _, command := range managedOnly.Commands() {
		testutil.Require(t, command.Name() != "set", "server-managed contexts exposed auth context set")
	}
	config.Bind(m)
	configDir := t.TempDir()
	t.Setenv("DEMO_CONFIG_DIR", configDir)
	hosts, err := config.LoadHosts()
	testutil.Require(t, err == nil, "%v", err)
	hosts.Set("api.example.com", config.HostEntry{AuthType: "bearer", OAuthToken: "token", Contexts: map[string]string{"organization": "org-1"}})
	testutil.NoError(t, hosts.Save())

	run := func(args ...string) (string, error) {
		t.Helper()
		var out strings.Builder
		root := &cobra.Command{Use: "demo"}
		root.SetOut(&out)
		root.SetErr(&out)
		root.PersistentFlags().String("hostname", "api.example.com", "")
		root.PersistentFlags().StringP("output", "o", "json", "")
		root.AddCommand(NewCommand(m))
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}

	if _, err := run("auth", "context", "set", "organization", "org-2"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := run("auth", "context", "set", "organization", "org-3", "-o", "bogus"); err == nil {
		t.Fatal("invalid output format succeeded")
	}
	t.Setenv("DEMO_ORG_ID", "org-env")
	out, err := run("auth", "context", "status")
	testutil.Require(t, err == nil, "status: %v", err)
	testutil.Require(t, strings.Contains(out, `"value": "org-env"`) && strings.Contains(out, `"source": "env"`), "status = %s", out)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	out, err = run("auth", "context", "status")
	testutil.Require(t, err == nil, "environment-only status: %v", err)
	testutil.Require(t, strings.Contains(out, `"value": "org-env"`) && strings.Contains(out, `"source": "env"`), "environment-only status = %s", out)
	if _, err := run("auth", "context", "unset", "organization"); err == nil || runtime.ClassifyError(err).Code != runtime.CodeNotAuthenticated {
		t.Fatalf("environment-only unset error = %v", err)
	}
	t.Setenv("DEMO_CONFIG_DIR", configDir)
	if _, err := run("auth", "context", "set", "workspace", "ws-2"); err == nil || runtime.ClassifyError(err).Code != runtime.CodeUsage {
		t.Fatalf("server-managed set error = %v", err)
	}
	reloaded, err := config.LoadHosts()
	testutil.Require(t, err == nil, "%v", err)
	entry, _ := reloaded.Get("api.example.com")
	testutil.Require(t, entry.Contexts["organization"] == "org-2" && entry.OAuthToken == "token", "entry = %+v", entry)
}

func TestOAuthDeviceRequestDistinguishesOmittedAndEmpty(t *testing.T) {
	fallback := map[string]string{"device_code": "device-1"}
	omitted, err := oauthDeviceRequest(nil, fallback, nil)
	testutil.Require(t, err == nil && omitted["device_code"] == "device-1", "omitted request = %#v, error = %v", omitted, err)
	empty, err := oauthDeviceRequest(map[string]string{}, fallback, nil)
	testutil.Require(t, err == nil && len(empty) == 0, "explicit empty request = %#v, error = %v", empty, err)
}

func TestStartBrowserCommandDoesNotWait(t *testing.T) {
	started := time.Now()
	testutil.NoError(t, startBrowserCommand(os.Args[0], "-test.run=^TestBrowserOpenerHelperProcess$", "--", "browser-opener-helper"))
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("startBrowserCommand waited %s", elapsed)
	}
}

func TestBrowserOpenerHelperProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "browser-opener-helper" {
		return
	}
	time.Sleep(3 * time.Second)
}

func TestAPIKeyLoginUsesManifestDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Auth-Token"); got != "secret" {
			t.Errorf("X-Auth-Token = %q, want secret", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Auth: config.AuthInfo{
			DefaultType:  "apikey",
			APIKeyHeader: "X-Auth-Token",
			Validate:     &config.AuthValidate{Path: "/", Assert: &config.AuthValidateAssert{Field: "ok", NonEmpty: true}},
		},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	stdin, input, err := os.Pipe()
	testutil.Require(t, err == nil, "%v", err)
	if _, err := input.WriteString("secret\n"); err != nil {
		t.Fatal(err)
	}
	testutil.NoError(t, input.Close())
	oldStdin := os.Stdin
	os.Stdin = stdin
	defer func() {
		stdin.Close()
		os.Stdin = oldStdin
	}()

	root := newAuthRoot(m, srv.URL)
	root.SetArgs([]string{"auth", "login", "--with-token"})
	testutil.NoError(t, root.Execute())
	hosts, err := config.LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
	entry, ok := hosts.Get(srv.URL)
	testutil.Require(t, ok, "host not saved")
	testutil.Require(t, entry.AuthType == "apikey" && entry.APIKey == "secret" && entry.APIKeyHeader == "X-Auth-Token", "entry = %+v", entry)
}

func TestOAuthDeviceLoginAcceptsAuthorizationPendingError(t *testing.T) {
	var tokenCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-1",
				"verification_uri": "https://example.com/device",
				"expires_in":       60,
				"interval":         1,
			})
		case "/token":
			tokenCalls++
			if tokenCalls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Auth: config.AuthInfo{Login: &config.AuthLogin{
			Type:      config.AuthLoginOAuthDevice,
			StartPath: "/start",
			TokenPath: "/token",
		}},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	root := newAuthRoot(m, srv.URL)
	root.SetArgs([]string{"auth", "login", "--device-auth"})

	testutil.NoError(t, root.Execute())
	testutil.Require(t, tokenCalls == 2, "tokenCalls = %d, want 2", tokenCalls)
	hosts, err := config.LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
	entry, ok := hosts.Get(srv.URL)
	testutil.Require(t, ok, "host not saved")
	testutil.Require(t, entry.AuthType == "bearer" && entry.OAuthToken == "access-1", "entry = %+v", entry)
}
