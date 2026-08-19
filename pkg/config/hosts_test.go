package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bindHostsTestManifest(t *testing.T) {
	t.Helper()
	Bind(&Manifest{CLI: CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"}})
}

func TestHostsRoundTripOAuthLoginFields(t *testing.T) {
	bindHostsTestManifest(t)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	hosts, err := LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
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
	if err := hosts.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts reload: %v", err)
	}
	entry, ok := loaded.Get("api.example.com")
	if !ok {
		t.Fatal("missing host")
	}
	if entry.AuthType != "bearer" || entry.LoginType != AuthLoginOAuthDevice || entry.LoginProvider != "github" || entry.OAuthToken != "access" || entry.OAuthRefreshToken != "refresh" || entry.OAuthExpiresAt != 1790000000 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Contexts["workspace"] != "ws-1" {
		t.Fatalf("contexts = %#v", entry.Contexts)
	}
}

func TestHostsPersistedDefaultRoundTrip(t *testing.T) {
	bindHostsTestManifest(t)
	dir := t.TempDir()
	t.Setenv("DEMO_CONFIG_DIR", dir)

	hosts, err := LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	hosts.Set("https://staging.example.com", HostEntry{AuthType: "bearer", OAuthToken: "secret-token"})
	hosts.Set("https://prod.example.com", HostEntry{AuthType: "bearer", OAuthToken: "other-token"})
	hosts.SetDefault("https://staging.example.com")
	if err := hosts.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "hosts.yml"))
	if err != nil {
		t.Fatalf("stat hosts.yml: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("hosts.yml mode = %o, want 0600", info.Mode().Perm())
	}

	raw, err := os.ReadFile(filepath.Join(dir, "hosts.yml"))
	if err != nil {
		t.Fatalf("read hosts.yml: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "default: staging.example.com") {
		t.Fatalf("missing default field:\n%s", text)
	}
	if strings.Count(text, "secret-token") != 1 || strings.Contains(text, "default: secret-token") {
		t.Fatalf("default field must not carry credentials:\n%s", text)
	}

	loaded, err := LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts reload: %v", err)
	}
	if loaded.Default() != "staging.example.com" {
		t.Fatalf("default = %q", loaded.Default())
	}
	if _, ok := loaded.Get("staging.example.com"); !ok {
		t.Fatal("missing staging host")
	}
	loaded.ClearDefault()
	if err := loaded.Save(); err != nil {
		t.Fatalf("Save after clear: %v", err)
	}
	reloaded, err := LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts after clear: %v", err)
	}
	if reloaded.Default() != "" {
		t.Fatalf("default after clear = %q", reloaded.Default())
	}
	if _, ok := reloaded.Get("staging.example.com"); !ok {
		t.Fatal("cleared default must not delete host credentials")
	}
}

func TestHostsLoadLegacyFileWithoutDefault(t *testing.T) {
	bindHostsTestManifest(t)
	dir := t.TempDir()
	t.Setenv("DEMO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte("api.example.com:\n  auth_type: bearer\n  oauth_token: token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	if loaded.Default() != "" {
		t.Fatalf("default = %q, want empty", loaded.Default())
	}
	entry, ok := loaded.Get("api.example.com")
	if !ok || entry.OAuthToken != "token" {
		t.Fatalf("entry = %+v, found = %v", entry, ok)
	}
}
