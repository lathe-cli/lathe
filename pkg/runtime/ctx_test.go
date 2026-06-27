package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func bindTestManifest(t *testing.T, name, hostEnv string) {
	t.Helper()
	config.Bind(&config.Manifest{CLI: config.CLIInfo{
		Name:         name,
		ConfigDir:    name,
		ConfigDirEnv: strings.ToUpper(name) + "_CONFIG_DIR",
		HostEnv:      hostEnv,
	}})
}

func TestNewNotAuthenticatedError_WrapsSentinel(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")
	err := NewNotAuthenticatedError()
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatal("expected errors.Is to match ErrNotAuthenticated")
	}
	if !strings.Contains(err.Error(), "demo host") || !strings.Contains(err.Error(), "`demo auth login`") {
		t.Errorf("expected rendered message to use bound name; got %q", err.Error())
	}
}

func TestResolveHost_UsesBoundHostEnv(t *testing.T) {
	bindTestManifest(t, "myapp", "MYAPP_HOST")
	t.Setenv("MYAPP_HOST", "example.internal")
	t.Setenv("OTHER_HOST", "should-be-ignored")

	root := &cobra.Command{Use: "myapp"}
	root.PersistentFlags().String("hostname", "", "")

	got, err := ResolveHost(root)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if got != "example.internal" {
		t.Errorf("want example.internal, got %q", got)
	}
}

func TestLoadHostOptionsRefreshesExpiredOAuthToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/refresh" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["refresh_token"] != "refresh-old" {
			t.Errorf("refresh_token = %q, want refresh-old", body["refresh_token"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	config.Bind(&config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Auth: config.AuthInfo{Login: &config.AuthLogin{
			Type:        config.AuthLoginOAuthDevice,
			StartPath:   "/start",
			TokenPath:   "/token",
			RefreshPath: "/refresh",
		}},
	})
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	hosts.Set(srv.URL, config.HostEntry{
		AuthType:          "bearer",
		OAuthToken:        "access-old",
		OAuthRefreshToken: "refresh-old",
		OAuthExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	})
	if err := hosts.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	root := &cobra.Command{Use: "demo"}
	root.SetContext(context.Background())
	root.PersistentFlags().String("hostname", srv.URL, "")
	root.PersistentFlags().Bool("insecure", false, "")

	hostname, opts, err := loadHostOptions(root, "")
	if err != nil {
		t.Fatalf("loadHostOptions: %v", err)
	}
	if hostname != config.NormalizeHostname(srv.URL) {
		t.Fatalf("hostname = %q", hostname)
	}
	auth, ok := opts.Auth.(BearerAuth)
	if !ok || auth.Token != "access-new" {
		t.Fatalf("auth = %#v", opts.Auth)
	}
	if opts.RefreshAuth == nil {
		t.Fatal("RefreshAuth is nil")
	}
	reloaded, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts reload: %v", err)
	}
	entry, ok := reloaded.Get(srv.URL)
	if !ok {
		t.Fatal("host missing")
	}
	if entry.OAuthToken != "access-new" || entry.OAuthRefreshToken != "refresh-new" || entry.OAuthExpiresAt <= time.Now().Unix() {
		t.Fatalf("entry = %+v", entry)
	}
}
