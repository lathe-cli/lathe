package lathecmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestInitRequiresLanguageOutsideTerminal(t *testing.T) {
	var output bytes.Buffer
	err := runInit([]string{"acme"}, strings.NewReader(""), false, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "--language") {
		t.Fatalf("error = %v", err)
	}
}

func TestInitAcceptsFlagsAfterTargetDirectory(t *testing.T) {
	t.Setenv("LATHE_INIT_VERSION", "v1.2.3")
	var output bytes.Buffer
	err := runInit([]string{"acme", "--language", "invalid"}, strings.NewReader(""), false, &output, &output)
	if err == nil || !strings.Contains(err.Error(), `unsupported language "invalid"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveInitVersionUsesBuildModuleVersion(t *testing.T) {
	got, err := resolveInitVersion("dev", "v0.4.5-0.20260720030934-ae4fa788b533+dirty", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.4.5-0.20260720030934-ae4fa788b533" {
		t.Fatalf("version = %q", got)
	}
}

func TestInitPromptsForTargetDirectoryInTerminal(t *testing.T) {
	t.Setenv("LATHE_INIT_VERSION", "v1.2.3")
	var output bytes.Buffer
	err := runInit([]string{"--language", "invalid"}, strings.NewReader("acme\n"), true, &output, &output)
	if err == nil || !strings.Contains(err.Error(), `unsupported language "invalid"`) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(output.String(), "Target directory [my-app]:") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestInitSelectsLanguageInTerminal(t *testing.T) {
	t.Setenv("LATHE_INIT_VERSION", "v1.2.3")
	var output bytes.Buffer
	err := runInit([]string{
		"--app-name", "Acme",
		"--cli-name", "acmectl",
		"--go-module", "example.com/acme",
		"--license", "none",
	}, strings.NewReader(".\n2\n"), true, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "target already exists") {
		t.Fatalf("error = %v", err)
	}
	for _, want := range []string{"Select language:", "1) Node.js", "2) Go", "3) Python", "4) Rust"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %q", want, output.String())
		}
	}
}

func TestResolveInitVersionAddsVPrefixToReleaseStamp(t *testing.T) {
	got, err := resolveInitVersion("0.5.0", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.5.0" {
		t.Fatalf("version = %q, want v0.5.0", got)
	}
}
