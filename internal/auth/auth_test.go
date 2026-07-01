package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
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
