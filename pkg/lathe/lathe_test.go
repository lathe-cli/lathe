package lathe

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestRootHelpExposesAgentHint(t *testing.T) {
	root := NewApp(testManifest())
	out, err := execute(root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"For agents:",
		"myctl commands --json",
		"myctl commands show",
		"myctl search",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestNewAppBindsManifest(t *testing.T) {
	m := testManifest()
	root := NewApp(m)
	if root.Use != "myctl" {
		t.Fatalf("root.Use = %q, want myctl", root.Use)
	}
	if got := config.Active(); got != m {
		t.Fatalf("bound manifest = %p, want %p", got, m)
	}
}

func TestNewAppAuthLoginAliasUsesBuiltinLoginPath(t *testing.T) {
	m, err := config.Load([]byte(`
cli:
  name: myctl
  short: test cli
auth:
  login_aliases: [login]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	root := NewApp(m)
	if child := findChild(root, "login"); child == nil {
		t.Fatal("root missing login alias")
	} else if child.Short != "Shortcut for auth login" {
		t.Fatalf("alias short = %q", child.Short)
	}

	restoreStdin := replaceStdin(t, "secret-token\n")
	defer restoreStdin()
	if _, err := execute(root, "login", "--hostname", "https://api.example.com", "--with-token", "--skip-validate"); err != nil {
		t.Fatalf("login alias execute: %v", err)
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	entry, ok := hosts.Get("api.example.com")
	if !ok {
		t.Fatal("expected saved host")
	}
	if entry.OAuthToken != "secret-token" {
		t.Fatalf("OAuthToken = %q, want secret-token", entry.OAuthToken)
	}

	root = NewApp(m)
	out, err := execute(root, "commands", "--json")
	if err != nil {
		t.Fatalf("commands: %v", err)
	}
	var catalog runtime.Catalog
	if err := json.Unmarshal([]byte(out), &catalog); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	if len(catalog.Commands) != 0 {
		t.Fatalf("alias should not appear in generated catalog: %+v", catalog.Commands)
	}

	out, err = execute(root, "search", "login", "--json")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var results []runtime.SearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("alias should not appear in generated search: %+v", results)
	}
}

func TestRunMountsAndExecutesGeneratedCommands(t *testing.T) {
	restoreVersionInfo(t)

	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n  short: test cli\n"),
		Mount: func(root *cobra.Command) error {
			root.AddCommand(&cobra.Command{
				Use: "ping",
				Run: func(cmd *cobra.Command, _ []string) {
					cmd.Print("pong")
				},
			})
			return nil
		},
		Version: "1.2.3",
		Commit:  "abc123",
		Date:    "2026-05-26",
	}, []string{"ping"}, &stdout, &stderr)
	if code != runtime.ExitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "pong" {
		t.Fatalf("stdout = %q, want pong", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n  short: test cli\n"),
		Version:  "1.2.3",
		Commit:   "abc123",
		Date:     "2026-05-26",
	}, []string{"version"}, &stdout, &stderr)
	if code != runtime.ExitOK {
		t.Fatalf("version exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "myctl 1.2.3 (abc123, 2026-05-26)\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func restoreVersionInfo(t *testing.T) {
	t.Helper()

	oldVersion := Version
	oldCommit := Commit
	oldDate := Date
	t.Cleanup(func() {
		Version = oldVersion
		Commit = oldCommit
		Date = oldDate
	})
}

func TestRunReportsInvalidManifest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{Manifest: []byte("cli: {}\n")}, []string{"--help"}, &stdout, &stderr)
	if code != runtime.ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, runtime.ExitGeneral)
	}
	if !strings.Contains(stderr.String(), "cli.name is required") {
		t.Fatalf("stderr missing manifest error: %q", stderr.String())
	}
}

func TestRunReportsMountError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n"),
		Mount: func(*cobra.Command) error {
			return errors.New("mount failed")
		},
	}, []string{"--help"}, &stdout, &stderr)
	if code != runtime.ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, runtime.ExitGeneral)
	}
	if !strings.Contains(stderr.String(), "mount failed") {
		t.Fatalf("stderr missing mount error: %q", stderr.String())
	}
}

func TestRunUsesRuntimeExecuteErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n"),
		Mount: func(root *cobra.Command) error {
			root.AddCommand(&cobra.Command{
				Use: "needs-auth",
				RunE: func(*cobra.Command, []string) error {
					return runtime.ErrNotAuthenticated
				},
			})
			return nil
		},
	}, []string{"needs-auth"}, &stdout, &stderr)
	if code != runtime.ExitNotAuthenticated {
		t.Fatalf("exit = %d, want %d", code, runtime.ExitNotAuthenticated)
	}
}

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func replaceStdin(t *testing.T, content string) func() {
	t.Helper()

	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("write stdin pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin pipe writer: %v", err)
	}
	os.Stdin = r
	return func() {
		os.Stdin = old
		_ = r.Close()
	}
}
