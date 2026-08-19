package auth

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func newAuthRoot(m *config.Manifest, hostname string) *cobra.Command {
	root := &cobra.Command{Use: "demo"}
	root.SetContext(context.Background())
	root.PersistentFlags().String("hostname", hostname, "")
	root.PersistentFlags().String("output", "table", "")
	root.PersistentFlags().Bool("insecure", false, "")
	root.AddCommand(NewCommand(m))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	return root
}

func selectedHost(t *testing.T) string {
	t.Helper()
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	return hosts.Selected()
}

func TestFirstLoginSelectsTheHostAndLaterOnesDoNot(t *testing.T) {
	m := &config.Manifest{
		CLI:  config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"},
		Auth: config.AuthInfo{DefaultType: "apikey", APIKeyHeader: "X-Auth-Token"},
	}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())

	login := func(hostname string) string {
		t.Helper()
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
			_ = stdin.Close()
			os.Stdin = oldStdin
		}()

		root := newAuthRoot(m, hostname)
		var stderr bytes.Buffer
		root.SetErr(&stderr)
		root.SetArgs([]string{"auth", "login", "--with-token"})
		if err := root.Execute(); err != nil {
			t.Fatalf("login %s: %v", hostname, err)
		}
		return stderr.String()
	}

	if out := login("first.example.com"); !strings.Contains(out, "Now using first.example.com") {
		t.Fatalf("stderr = %q, want the first login to announce the selection", out)
	}
	if got := selectedHost(t); got != "first.example.com" {
		t.Fatalf("selected = %q, want the first login", got)
	}
	login("second.example.com")
	if got := selectedHost(t); got != "first.example.com" {
		t.Fatalf("selected = %q, want the first login to stand", got)
	}
	if out := login("first.example.com"); strings.Contains(out, "Now using") {
		t.Fatalf("stderr = %q, want a re-login not to re-elect", out)
	}

	root := newAuthRoot(m, "")
	root.SetArgs([]string{"auth", "use", "https://second.example.com"})
	if err := root.Execute(); err != nil {
		t.Fatalf("auth use: %v", err)
	}
	if got := selectedHost(t); got != "second.example.com" {
		t.Errorf("selected = %q, want the explicit switch", got)
	}
}

func TestAuthUseRejectsAHostThatIsNotLoggedIn(t *testing.T) {
	m := &config.Manifest{CLI: config.CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"}}
	config.Bind(m)
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	hosts.Set("a.example.com", config.HostEntry{AuthType: "bearer"})
	if err := hosts.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	root := newAuthRoot(m, "")
	root.SetArgs([]string{"auth", "use", "missing.example.com"})
	err = root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v, want a not-logged-in refusal", err)
	}
	if got := selectedHost(t); got != "" {
		t.Errorf("selected = %q, want the selection untouched", got)
	}
}
