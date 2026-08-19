package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func bindHostTestManifest(t *testing.T) *config.Manifest {
	t.Helper()
	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	t.Setenv("DEMO_HOST", "")
	return m
}

func saveTestHosts(t *testing.T, defaultHost string, names ...string) {
	t.Helper()
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		hosts.Set(name, config.HostEntry{AuthType: "bearer", OAuthToken: "tok", User: "octo"})
	}
	if defaultHost != "" {
		hosts.SetDefault(defaultHost)
	}
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}
}

func execAuthRoot(t *testing.T, m *config.Manifest, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := &cobra.Command{Use: "demo"}
	root.SetOut(&out)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.AddCommand(NewHostCommand())
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func loginWithToken(t *testing.T, m *config.Manifest, hostname, token string) {
	t.Helper()
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString(token + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	defer func() {
		_ = stdin.Close()
		os.Stdin = oldStdin
	}()

	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", hostname, "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--with-token", "--skip-validate"})
	if err := root.Execute(); err != nil {
		t.Fatalf("login %s: %v", hostname, err)
	}
}

func TestHostDefaultSetShowUnset(t *testing.T) {
	m := bindHostTestManifest(t)
	saveTestHosts(t, "", "prod.example.com", "staging.example.com")

	out, err := execAuthRoot(t, m, "host", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No default host is set") {
		t.Fatalf("default = %q", out)
	}

	if _, err := execAuthRoot(t, m, "host", "default", "set", "staging.example.com"); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err = execAuthRoot(t, m, "host", "default")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "staging.example.com" {
		t.Fatalf("default after set = %q", out)
	}

	if _, err := execAuthRoot(t, m, "host", "default", "set", "missing.example.com"); err == nil {
		t.Fatal("set unknown host succeeded")
	}

	if _, err := execAuthRoot(t, m, "host", "default", "unset"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	if hosts.Default() != "" {
		t.Fatalf("default after unset = %q", hosts.Default())
	}
}

func TestAuthLoginElectsDefaultOnlyWhenUnset(t *testing.T) {
	m := bindHostTestManifest(t)

	loginWithToken(t, m, "first.example.com", "token-one")
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	if hosts.Default() != "first.example.com" {
		t.Fatalf("first login default = %q", hosts.Default())
	}

	loginWithToken(t, m, "second.example.com", "token-two")
	hosts, err = config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	if hosts.Default() != "first.example.com" {
		t.Fatalf("later login overrode default: %q", hosts.Default())
	}
	if _, ok := hosts.Get("second.example.com"); !ok {
		t.Fatal("second host not saved")
	}
}

func TestAuthStatusJSONExposesHostProvenance(t *testing.T) {
	m := bindHostTestManifest(t)
	saveTestHosts(t, "staging.example.com", "prod.example.com", "staging.example.com")

	out, err := execAuthRoot(t, m, "auth", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hostname string `json:"hostname"`
		Source   string `json:"source"`
		Default  string `json:"default"`
		Hosts    []struct {
			Hostname string `json:"hostname"`
			Auth     string `json:"auth"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Hostname != "staging.example.com" || got.Source != "persisted" || got.Default != "staging.example.com" {
		t.Fatalf("status = %#v\n%s", got, out)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %#v", got.Hosts)
	}
}
