package config

import (
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestHostsRoundTripOAuthLoginFields(t *testing.T) {
	m := &Manifest{CLI: CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"}}
	Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	hosts, err := LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
	hosts.Set("other.example.com", HostEntry{AuthType: "bearer"})
	hosts.Select("other.example.com")
	hosts.Set("https://api.example.com", HostEntry{
		AuthType:          "bearer",
		LoginType:         AuthLoginOAuthDevice,
		LoginProvider:     "github",
		User:              "octo@example.com",
		OAuthToken:        "access",
		OAuthRefreshToken: "refresh",
		OAuthExpiresAt:    1790000000,
		Contexts:          map[string]string{"workspace": "ws-1"},
	})
	hosts.Select("https://api.example.com")
	testutil.NoError(t, hosts.Save())

	loaded, err := LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts reload: %v", err)
	entry, ok := loaded.Get("api.example.com")
	testutil.Require(t, ok, "missing host")
	testutil.Require(t, entry.AuthType == "bearer" && entry.LoginType == AuthLoginOAuthDevice && entry.LoginProvider == "github" && entry.OAuthToken == "access" && entry.OAuthRefreshToken == "refresh" && entry.OAuthExpiresAt == 1790000000, "entry = %+v", entry)
	testutil.Require(t, entry.Contexts["workspace"] == "ws-1", "contexts = %#v", entry.Contexts)
	if got := loaded.Selected(); got != "api.example.com" {
		t.Fatalf("Selected = %q, want the last selection to be the only one", got)
	}
}
