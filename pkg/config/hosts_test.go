package config

import "testing"

func TestHostsRoundTripOAuthLoginFields(t *testing.T) {
	m := &Manifest{CLI: CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"}}
	Bind(m)
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
