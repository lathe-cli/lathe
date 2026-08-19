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

func bindAuthTestManifest(t *testing.T) *config.Manifest {
	t.Helper()
	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	t.Setenv("DEMO_HOST", "")
	return m
}

func saveAuthHosts(t *testing.T, defaultHost string, names ...string) {
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

func execRoot(t *testing.T, m *config.Manifest, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := &cobra.Command{Use: "demo"}
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.AddCommand(NewCommand(m))
	root.AddCommand(NewHostCommand())
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String() + captureIfNeeded(), err
}

func captureIfNeeded() string { return "" }

func TestHostDefaultSetAndUnset(t *testing.T) {
	m := bindAuthTestManifest(t)
	saveAuthHosts(t, "", "prod.example.com", "staging.example.com")

	out, _, err := execRoot(t, m, "host", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No default host is set") {
		t.Fatalf("default = %q", out)
	}

	if _, _, err := execRoot(t, m, "host", "default", "set", "staging.example.com"); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, _, err = execRoot(t, m, "host", "default")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "staging.example.com" {
		t.Fatalf("default after set = %q", out)
	}

	if _, _, err := execRoot(t, m, "host", "default", "set", "missing.example.com"); err == nil {
		t.Fatal("set unknown host succeeded")
	}

	if _, _, err := execRoot(t, m, "host", "default", "unset"); err != nil {
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
	m := bindAuthTestManifest(t)
	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", "first.example.com", "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--with-token", "--skip-validate"})
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString("token-one\n"); err != nil {
		t.Fatal(err)
	}
	input.Close()
	old := os.Stdin
	os.Stdin = stdin
	if err := root.Execute(); err != nil {
		os.Stdin = old
		t.Fatalf("first login: %v", err)
	}
	os.Stdin = old
	stdin.Close()

	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	if hosts.Default() != "first.example.com" {
		t.Fatalf("first login default = %q", hosts.Default())
	}

	root = &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", "second.example.com", "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetArgs([]string{"auth", "login", "--with-token", "--skip-validate"})
	stdin, input, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString("token-two\n"); err != nil {
		t.Fatal(err)
	}
	input.Close()
	os.Stdin = stdin
	if err := root.Execute(); err != nil {
		os.Stdin = old
		t.Fatalf("second login: %v", err)
	}
	os.Stdin = old
	stdin.Close()

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
	m := bindAuthTestManifest(t)
	saveAuthHosts(t, "staging.example.com", "prod.example.com", "staging.example.com")
	out, _, err := execRoot(t, m, "auth", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hostname string `json:"hostname"`
		Source   string `json:"source"`
		Default  string `json:"default"`
		Hosts    []struct {
			Hostname string `json:"hostname"`
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
