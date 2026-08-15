package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
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
			if body["provider"] != "github" {
				t.Errorf("provider = %q, want github", body["provider"])
			}
			if body["hostname"] == "" {
				t.Error("hostname missing")
			}
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
			if body["device_code"] != "device-1" {
				t.Errorf("device_code = %q, want device-1", body["device_code"])
			}
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

	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", srv.URL, "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--auth-type", "oauth", "--provider", "github"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !startCalled || !tokenCalled {
		t.Fatalf("startCalled=%v tokenCalled=%v", startCalled, tokenCalled)
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	entry, ok := hosts.Get(srv.URL)
	if !ok {
		t.Fatal("host not saved")
	}
	if entry.AuthType != "bearer" || entry.LoginType != config.AuthLoginOAuthDevice || entry.LoginProvider != "github" || entry.OAuthToken != "access-1" || entry.OAuthRefreshToken != "refresh-1" || entry.User != "octo@example.com" || entry.OAuthExpiresAt == 0 {
		t.Fatalf("entry = %+v", entry)
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
	if err != nil {
		t.Fatal(err)
	}
	hosts.Set(srv.URL, config.HostEntry{AuthType: "bearer", OAuthToken: "old", Contexts: map[string]string{"organization": "org-old", "workspace": "ws-old"}})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}

	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", srv.URL, "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--device-auth", "--no-browser", "--skip-validate"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if startBody["client_id"] != "demo-cli" || !strings.HasPrefix(startBody["device_label"], "demo on ") || len(startBody) != 2 {
		t.Fatalf("start body = %#v", startBody)
	}
	if pollBody["client_id"] != "demo-cli" || pollBody["device_code"] != "device-1" || len(pollBody) != 2 {
		t.Fatalf("poll body = %#v", pollBody)
	}
	hosts, err = config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	entry, ok := hosts.Get(srv.URL)
	if !ok || entry.OAuthToken != "access-1" || entry.Contexts["workspace"] != "ws-1" || entry.Contexts["organization"] != "org-concurrent" {
		t.Fatalf("entry = %+v, found = %v", entry, ok)
	}
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
		if command.Name() == "set" {
			t.Fatal("server-managed contexts exposed auth context set")
		}
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	hosts.Set("api.example.com", config.HostEntry{AuthType: "bearer", OAuthToken: "token", Contexts: map[string]string{"organization": "org-1"}})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}

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
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, `"value": "org-env"`) || !strings.Contains(out, `"source": "env"`) {
		t.Fatalf("status = %s", out)
	}
	if _, err := run("auth", "context", "set", "workspace", "ws-2"); err == nil || runtime.ClassifyError(err).Code != runtime.CodeUsage {
		t.Fatalf("server-managed set error = %v", err)
	}
	reloaded, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := reloaded.Get("api.example.com")
	if entry.Contexts["organization"] != "org-2" || entry.OAuthToken != "token" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestOAuthDeviceRequestDistinguishesOmittedAndEmpty(t *testing.T) {
	fallback := map[string]string{"device_code": "device-1"}
	omitted, err := oauthDeviceRequest(nil, fallback, nil)
	if err != nil || omitted["device_code"] != "device-1" {
		t.Fatalf("omitted request = %#v, error = %v", omitted, err)
	}
	empty, err := oauthDeviceRequest(map[string]string{}, fallback, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("explicit empty request = %#v, error = %v", empty, err)
	}
}

func TestStartBrowserCommandDoesNotWait(t *testing.T) {
	started := time.Now()
	if err := startBrowserCommand(os.Args[0], "-test.run=^TestBrowserOpenerHelperProcess$", "--", "browser-opener-helper"); err != nil {
		t.Fatalf("startBrowserCommand: %v", err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString("secret\n"); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	defer func() {
		stdin.Close()
		os.Stdin = oldStdin
	}()

	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", srv.URL, "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--with-token"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	entry, ok := hosts.Get(srv.URL)
	if !ok {
		t.Fatal("host not saved")
	}
	if entry.AuthType != "apikey" || entry.APIKey != "secret" || entry.APIKeyHeader != "X-Auth-Token" {
		t.Fatalf("entry = %+v", entry)
	}
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

	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", srv.URL, "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--device-auth"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tokenCalls != 2 {
		t.Fatalf("tokenCalls = %d, want 2", tokenCalls)
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	entry, ok := hosts.Get(srv.URL)
	if !ok {
		t.Fatal("host not saved")
	}
	if entry.AuthType != "bearer" || entry.OAuthToken != "access-1" {
		t.Fatalf("entry = %+v", entry)
	}
}
