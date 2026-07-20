package projectinit

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesUncommittedRepositoryFromTemplateRef(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "template")
	mustMkdirAll(t, filepath.Join(source, "cmd", "appctl"))
	mustWrite(t, filepath.Join(source, "README.md"), "# Starter app\n")
	mustWrite(t, filepath.Join(source, "cli.yaml"), "cli:\n  name: appctl\n")
	mustWrite(t, filepath.Join(source, "cmd", "appctl", "cli.yaml"), "cli:\n  name: appctl\n")
	mustWrite(t, filepath.Join(source, "go.mod"), "module example.com/starter\n\ngo 1.25.7\n")
	mustWrite(t, filepath.Join(source, "LATHE_VERSION"), "v0.4.4\n")
	mustWrite(t, filepath.Join(source, "PACKAGE_IDENT"), "starter_app\n")
	mustWrite(t, filepath.Join(source, manifestName), `schema_version: 1
language: node
defaults:
  app_name: Starter app
  cli_name: appctl
  go_module: example.com/starter
  lathe_version: v0.4.4
replacements:
  - variable: app_name
    files: [README.md]
  - variable: cli_name
    files: [cli.yaml, cmd/appctl/cli.yaml]
  - variable: go_module
    files: [go.mod]
  - variable: lathe_version
    files: [LATHE_VERSION]
  - variable: package_ident
    from: starter_app
    files: [PACKAGE_IDENT]
renames:
  - from: cmd/appctl
    variable: cli_name
generated: [internal/generated, skills/appctl]
cleanup: [.cache, bin]
check_profile: pnpm
`)
	runTestCommand(t, source, "git", "init", "-b", "main")
	runTestCommand(t, source, "git", "config", "user.name", "Lathe Test")
	runTestCommand(t, source, "git", "config", "user.email", "lathe@example.com")
	runTestCommand(t, source, "git", "add", ".")
	runTestCommand(t, source, "git", "commit", "-m", "template")
	runTestCommand(t, source, "git", "switch", "-c", "next")
	mustWrite(t, filepath.Join(source, "REF"), "next\n")
	runTestCommand(t, source, "git", "add", "REF")
	runTestCommand(t, source, "git", "commit", "-m", "next")
	runTestCommand(t, source, "git", "tag", "v1.0.0")
	wantCommit := strings.TrimSpace(runTestCommand(t, source, "git", "rev-parse", "HEAD"))

	target := filepath.Join(parent, "acme")
	result, err := Init(Options{
		Target:        target,
		Language:      "node",
		Template:      source + "#v1.0.0",
		AppName:       "Acme",
		CLIName:       "acmectl",
		GoModule:      "example.com/acme",
		License:       "none",
		LicenseHolder: "Acme contributors",
		LatheVersion:  "v1.2.3",
		Stderr:        io.Discard,
		Bootstrap: func(root string, _ io.Writer) error {
			return os.MkdirAll(filepath.Join(root, "internal", "generated"), 0o755)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Template.Ref != "v1.0.0" || result.Template.Commit != wantCommit {
		t.Fatalf("template result = %#v", result.Template)
	}
	if got := mustRead(t, filepath.Join(target, "README.md")); got != "# Acme\n" {
		t.Fatalf("README = %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "go.mod")); !strings.Contains(got, "module example.com/acme") {
		t.Fatalf("go.mod = %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "LATHE_VERSION")); got != "v1.2.3\n" {
		t.Fatalf("LATHE_VERSION = %q", got)
	}
	if got := mustRead(t, filepath.Join(target, "PACKAGE_IDENT")); got != "acme\n" {
		t.Fatalf("PACKAGE_IDENT = %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "cmd", "acmectl", "cli.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{manifestName, "LICENSE", "cmd/appctl"} {
		if _, err := os.Stat(filepath.Join(target, absent)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be absent, got %v", absent, err)
		}
	}
	if out := strings.TrimSpace(runTestCommand(t, target, "git", "remote")); out != "" {
		t.Fatalf("remote = %q", out)
	}
	if err := exec.Command("git", "-C", target, "rev-parse", "--verify", "HEAD").Run(); err == nil {
		t.Fatal("initialized repository has a commit")
	}
	if out := strings.TrimSpace(runTestCommand(t, target, "git", "diff", "--cached", "--name-only")); out != "" {
		t.Fatalf("staged files = %q", out)
	}
}

func TestWriteMITLicense(t *testing.T) {
	root := t.TempDir()
	if err := writeLicense(root, "mit", "Acme, Inc."); err != nil {
		t.Fatal(err)
	}
	license := mustRead(t, filepath.Join(root, "LICENSE"))
	if !strings.Contains(license, "MIT License") || !strings.Contains(license, "Acme, Inc.") {
		t.Fatalf("LICENSE = %q", license)
	}
}

func TestManifestRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, manifestName), "schema_version: 1\nlanguage: node\nunknown: true\n")
	if _, err := loadManifest(root); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("error = %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runTestCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}
