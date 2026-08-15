package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestClassifyError_Nil(t *testing.T) {
	if ClassifyError(nil) != nil {
		t.Fatal("expected nil for nil error")
	}
}

func TestClassifyError_NotAuthenticated(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", ErrNotAuthenticated)
	le := ClassifyError(err)
	if le.Code != CodeNotAuthenticated {
		t.Errorf("code = %q, want %q", le.Code, CodeNotAuthenticated)
	}
	if le.ExitCode != ExitNotAuthenticated {
		t.Errorf("exit = %d, want %d", le.ExitCode, ExitNotAuthenticated)
	}
	if le.Hint == "" {
		t.Fatal("authentication error missing hint")
	}
}

func TestClassifyError_HTTPError(t *testing.T) {
	err := &HTTPError{Method: "GET", URL: "/private", Status: 500, Body: []byte("upstream-secret")}
	le := ClassifyError(err)
	if le.Code != CodeAPIError {
		t.Errorf("code = %q, want %q", le.Code, CodeAPIError)
	}
	if le.ExitCode != ExitAPIError {
		t.Errorf("exit = %d, want %d", le.ExitCode, ExitAPIError)
	}
	if le.HTTP == nil || le.HTTP.Status != 500 {
		t.Fatalf("http context = %#v, want status 500", le.HTTP)
	}
	if strings.Contains(le.Message, "upstream-secret") || strings.Contains(le.Message, "/private") {
		t.Fatalf("machine message exposed HTTP details: %q", le.Message)
	}
}

func TestClassifyError_Passthrough(t *testing.T) {
	orig := NewLatheError(CodeUsage, ExitUsage, errors.New("bad flag"))
	le := ClassifyError(orig)
	if le != orig {
		t.Error("expected same LatheError instance returned")
	}
}

func TestClassifyError_Generic(t *testing.T) {
	le := ClassifyError(errors.New("boom"))
	if le.Code != CodeGeneral {
		t.Errorf("code = %q, want %q", le.Code, CodeGeneral)
	}
	if le.ExitCode != ExitGeneral {
		t.Errorf("exit = %d, want %d", le.ExitCode, ExitGeneral)
	}
	if le.Message != "command failed" || le.Hint == "" {
		t.Fatalf("generic machine error = %#v", le)
	}
}

func TestFormatError_JSON(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(errors.New("oops"), "json", &buf)
	if code != ExitGeneral {
		t.Errorf("exit = %d, want %d", code, ExitGeneral)
	}
	var env errorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if env.Error.Code != CodeGeneral {
		t.Errorf("json code = %q, want %q", env.Error.Code, CodeGeneral)
	}
	if env.Error.Message != "command failed" || env.Error.Hint == "" {
		t.Errorf("json error = %#v", env.Error)
	}
}

func TestFormatError_YAML(t *testing.T) {
	var buf bytes.Buffer
	cause := &HTTPError{Method: "POST", URL: "https://example.invalid/private", Status: 429, Body: []byte("upstream-secret")}
	if code := FormatError(cause, "yaml", &buf); code != ExitAPIError {
		t.Fatalf("exit = %d, want %d", code, ExitAPIError)
	}
	var env errorEnvelope
	if err := yaml.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, buf.String())
	}
	if env.Error.Code != CodeAPIError || env.Error.Hint == "" || env.Error.HTTP == nil || env.Error.HTTP.Status != 429 {
		t.Fatalf("yaml error = %#v", env.Error)
	}
	if strings.Contains(buf.String(), "upstream-secret") || strings.Contains(buf.String(), "private") {
		t.Fatalf("YAML leaked HTTP details: %s", buf.String())
	}
}

func TestFormatError_Plain(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(errors.New("oops"), "table", &buf)
	if code != ExitGeneral {
		t.Errorf("exit = %d, want %d", code, ExitGeneral)
	}
	if !strings.Contains(buf.String(), "Error: command failed") || !strings.Contains(buf.String(), "Hint:") || strings.Contains(buf.String(), "oops") {
		t.Errorf("plain error = %q", buf.String())
	}
}

func TestFormatError_Nil(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(nil, "json", &buf)
	if code != ExitOK {
		t.Errorf("exit = %d, want %d", code, ExitOK)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output for nil error, got %q", buf.String())
	}
}

func TestLatheError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	le := NewLatheError(CodeGeneral, ExitGeneral, cause)
	if !errors.Is(le, cause) {
		t.Error("expected Unwrap to expose cause")
	}
}

func TestClassifyError_Canceled(t *testing.T) {
	le := ClassifyError(fmt.Errorf("request: %w", context.Canceled))
	if le.Code != CodeCanceled || le.ExitCode != ExitCanceled || le.Hint == "" {
		t.Fatalf("canceled error = %#v", le)
	}
}

type testSilentExitError struct{}

func (testSilentExitError) Error() string {
	return "hidden"
}

func (testSilentExitError) SilentExitCode() int {
	return ExitUsage
}

func TestExecuteSilentExitError(t *testing.T) {
	cmd := &cobra.Command{
		Use: "demo",
		RunE: func(*cobra.Command, []string) error {
			return testSilentExitError{}
		},
	}
	if code := Execute(cmd); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}
