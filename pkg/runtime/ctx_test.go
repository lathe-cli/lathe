package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"

	"github.com/lathe-cli/lathe/internal/testutil"
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
	testutil.Require(t, errors.Is(err, ErrNotAuthenticated), "expected errors.Is to match ErrNotAuthenticated")
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
	testutil.Require(t, err == nil, "ResolveHost: %v", err)
	testutil.Check(t, got == "example.internal", "want example.internal, got %q", got)
}

func TestLoadHostOptionsRefreshesExpiredOAuthToken(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/refresh" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		testutil.Check(t, body["refresh_token"] == "refresh-old", "refresh_token = %q, want refresh-old", body["refresh_token"])
		if err := config.MutateHosts(r.Context(), func(hosts *config.Hosts) error {
			entry, _ := hosts.Get(srv.URL)
			entry.Contexts["workspace"] = "ws-new"
			hosts.Set(srv.URL, entry)
			return nil
		}); err != nil {
			t.Errorf("update context during refresh: %v", err)
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
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
	hosts.Set(srv.URL, config.HostEntry{
		AuthType:          "bearer",
		OAuthToken:        "access-old",
		OAuthRefreshToken: "refresh-old",
		OAuthExpiresAt:    time.Now().Add(-time.Hour).Unix(),
		Contexts:          map[string]string{"workspace": "ws-old"},
	})
	testutil.NoError(t, hosts.Save())

	root := &cobra.Command{Use: "demo"}
	root.SetContext(context.Background())
	root.PersistentFlags().String("hostname", srv.URL, "")
	root.PersistentFlags().Bool("insecure", false, "")

	host, opts, err := loadHostOptions(root, "", true)
	testutil.Require(t, err == nil, "loadHostOptions: %v", err)
	testutil.Require(t, host.Hostname == config.NormalizeHostname(srv.URL), "hostname = %q", host.Hostname)
	auth, ok := opts.Auth.(BearerAuth)
	testutil.Require(t, ok && auth.Token == "access-new", "auth = %#v", opts.Auth)
	testutil.Require(t, opts.RefreshAuth != nil, "RefreshAuth is nil")
	reloaded, err := config.LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts reload: %v", err)
	entry, ok := reloaded.Get(srv.URL)
	testutil.Require(t, ok, "host missing")
	testutil.Require(t, entry.OAuthToken == "access-new" && entry.OAuthRefreshToken == "refresh-new" && entry.OAuthExpiresAt > time.Now().Unix(), "entry = %+v", entry)
	testutil.Require(t, entry.Contexts["workspace"] == "ws-new", "context overwritten during refresh: %+v", entry.Contexts)
}

func TestHostOptionsPreservesOptionalAuthPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "refresh rejected", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name         string
		entry        *config.HostEntry
		wantToken    string
		wantInsecure bool
	}{
		{name: "missing", wantInsecure: true},
		{name: "invalid", entry: &config.HostEntry{AuthType: "unknown", Insecure: true}},
		{name: "failed refresh", entry: &config.HostEntry{AuthType: "bearer", OAuthToken: "old", OAuthRefreshToken: "refresh", OAuthExpiresAt: 1, Insecure: true}, wantToken: "old", wantInsecure: true},
	} {
		for _, optional := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optional=%t", tc.name, optional), func(t *testing.T) {
				config.Bind(&config.Manifest{
					CLI:  config.CLIInfo{Name: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
					Auth: config.AuthInfo{Login: &config.AuthLogin{RefreshPath: "/refresh"}},
				})
				t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
				hosts, err := config.LoadHosts()
				testutil.Require(t, err == nil, "%v", err)
				if tc.entry != nil {
					hosts.Set(srv.URL, *tc.entry)
					testutil.NoError(t, hosts.Save())
				}
				root := newExecutionRoot("raw")
				root.PersistentFlags().Bool("insecure", true, "")
				testutil.NoError(t, root.PersistentFlags().Set("hostname", srv.URL))
				host, opts, err := hostOptions(root, "", true, optional)
				testutil.Require(t, err == nil == optional, "optional=%t, error=%v", optional, err)
				testutil.Require(t, host.Hostname == srv.URL && host.Source == HostSourceFlag, "host=%+v", host)
				testutil.Require(t, opts.Insecure == (optional && tc.wantInsecure), "insecure=%t", opts.Insecure)
				if optional && tc.wantToken != "" {
					auth, ok := opts.Auth.(BearerAuth)
					testutil.Require(t, ok && auth.Token == tc.wantToken && opts.RefreshAuth != nil, "auth=%#v, refresh=%v", opts.Auth, opts.RefreshAuth != nil)
				} else if opts.Auth != nil || opts.RefreshAuth != nil {
					t.Fatalf("unexpected auth=%#v", opts.Auth)
				}
			})
		}
	}
}
