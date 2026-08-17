package lathe

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"gopkg.in/yaml.v3"
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

func TestTopLevelCompletionExposed(t *testing.T) {
	root := NewApp(testManifest())

	out, err := execute(root, "completion", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# bash completion V2 for myctl") {
		t.Fatalf("completion bash output missing script header:\n%s", out)
	}

	help, err := execute(root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help, "completion") {
		t.Fatalf("--help missing completion:\n%s", help)
	}

	meta, _, err := root.Find([]string{metaCommandName, "completion", "bash"})
	if err != nil || meta == nil || meta.Name() != "bash" {
		t.Fatalf("%s completion missing: %v", metaCommandName, err)
	}

	catalogOut, err := execute(root, "commands", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(catalogOut, "completion") {
		t.Fatalf("catalog must not list framework completion commands:\n%s", catalogOut)
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
	}, []string{metaCommandName, "version"}, &stdout, &stderr)
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
	if got := stderr.String(); got != "Error: invalid CLI configuration\nHint: fix cli.yaml and retry\n" {
		t.Fatalf("stderr = %q", got)
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
	if got := stderr.String(); got != "Error: generated CLI failed to start\nHint: re-run code generation and rebuild the CLI\n" {
		t.Fatalf("stderr = %q", got)
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

func TestRunMachineErrorContract(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		cause    error
		wantCode string
		wantExit int
		status   int
	}{
		{name: "cobra usage", args: []string{"unknown-secret-command"}, wantCode: runtime.CodeUsage, wantExit: runtime.ExitUsage},
		{name: "nested cobra usage", args: []string{"__lathe", "unknown-secret-command"}, wantCode: runtime.CodeUsage, wantExit: runtime.ExitUsage},
		{name: "extra cobra argument", args: []string{"__lathe", "version", "unknown-secret-command"}, wantCode: runtime.CodeUsage, wantExit: runtime.ExitUsage},
		{name: "auth", args: []string{"fail"}, cause: runtime.ErrNotAuthenticated, wantCode: runtime.CodeNotAuthenticated, wantExit: runtime.ExitNotAuthenticated},
		{name: "api", args: []string{"fail"}, cause: &runtime.HTTPError{Method: "GET", URL: "/private", Status: 429, Body: []byte("upstream-secret")}, wantCode: runtime.CodeAPIError, wantExit: runtime.ExitAPIError, status: 429},
		{name: "canceled", args: []string{"fail"}, cause: context.Canceled, wantCode: runtime.CodeCanceled, wantExit: runtime.ExitCanceled},
	}
	for _, format := range []string{"json", "yaml"} {
		for _, tc := range tests {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				args := append([]string{"--output", format}, tc.args...)
				code := run(RunOptions{
					Manifest: []byte("cli:\n  name: myctl\n"),
					Mount: func(root *cobra.Command) error {
						if tc.cause != nil {
							root.AddCommand(&cobra.Command{Use: "fail", RunE: func(*cobra.Command, []string) error { return tc.cause }})
						}
						return nil
					},
				}, args, &stdout, &stderr)
				if code != tc.wantExit {
					t.Fatalf("exit = %d, want %d; stderr = %s", code, tc.wantExit, stderr.String())
				}
				var env struct {
					Error runtime.LatheError `yaml:"error"`
				}
				if err := yaml.Unmarshal(stderr.Bytes(), &env); err != nil {
					t.Fatalf("decode %s: %v\n%s", format, err, stderr.String())
				}
				if env.Error.Code != tc.wantCode || env.Error.Message == "" || env.Error.Hint == "" {
					t.Fatalf("error = %#v", env.Error)
				}
				if tc.status != 0 && (env.Error.HTTP == nil || env.Error.HTTP.Status != tc.status) {
					t.Fatalf("http context = %#v, want status %d", env.Error.HTTP, tc.status)
				}
				for _, secret := range []string{"unknown-secret-command", "upstream-secret", "/private"} {
					if strings.Contains(stderr.String(), secret) {
						t.Fatalf("machine error leaked %q: %s", secret, stderr.String())
					}
				}
			})
		}
	}
}

func TestRunFormatsStartupErrorAsMachineOutput(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(RunOptions{Manifest: []byte("cli: {}\n")}, []string{"-o=" + format}, &stdout, &stderr)
			if code != runtime.ExitGeneral {
				t.Fatalf("exit = %d, want %d", code, runtime.ExitGeneral)
			}
			var env struct {
				Error runtime.LatheError `yaml:"error"`
			}
			if err := yaml.Unmarshal(stderr.Bytes(), &env); err != nil {
				t.Fatalf("decode %s: %v\n%s", format, err, stderr.String())
			}
			if env.Error.Code != runtime.CodeGeneral || env.Error.Hint == "" {
				t.Fatalf("startup error = %#v", env.Error)
			}
		})
	}
}
