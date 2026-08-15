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
}

func TestClassifyError_HTTPError(t *testing.T) {
	err := &HTTPError{Method: "GET", URL: "/x", Status: 422, Body: []byte(`{"error":"invalid input","detail":"token=body-secret; Bearer body-bearer","token":"server-secret"}`)}
	le := ClassifyError(err)
	if le.Code != CodeAPIError {
		t.Errorf("code = %q, want %q", le.Code, CodeAPIError)
	}
	if le.ExitCode != ExitAPIError {
		t.Errorf("exit = %d, want %d", le.ExitCode, ExitAPIError)
	}
	if le.HTTPStatus != 422 || le.Method != "GET" || le.URL != "/x" {
		t.Errorf("HTTP context = %+v", le)
	}
	if le.ServerBody != `{"detail":"token=***; Bearer ***","error":"invalid input","token":"***"}` {
		t.Errorf("server body = %q", le.ServerBody)
	}
	if strings.Contains(le.Message+le.ServerBody, "server-secret") || strings.Contains(le.ServerBody, "body-secret") || strings.Contains(le.ServerBody, "body-bearer") {
		t.Fatalf("classified error leaked server secret: %+v", le)
	}
	if strings.Contains(err.Error(), "server-secret") {
		t.Fatalf("HTTPError.Error leaked response body: %q", err.Error())
	}

	large := ClassifyError(&HTTPError{Status: 500, Body: []byte(`{"message":"` + strings.Repeat("x", 2048) + `"}`)})
	if len([]rune(large.ServerBody)) > 1025 {
		t.Fatalf("server body is not bounded: %d runes", len([]rune(large.ServerBody)))
	}
	plain := ClassifyError(&HTTPError{Status: 500, Body: []byte("token=server-secret")})
	if plain.ServerBody != "" {
		t.Fatalf("non-JSON server body was exposed: %q", plain.ServerBody)
	}
}

func TestClassifyError_Canceled(t *testing.T) {
	le := ClassifyError(fmt.Errorf("request stopped: %w", context.Canceled))
	if le.Code != CodeCanceled || le.ExitCode != ExitCanceled {
		t.Fatalf("classified canceled error = %+v", le)
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
}

func TestFormatError_JSON(t *testing.T) {
	var buf bytes.Buffer
	err := NewLatheError(CodeUsage, ExitUsage, errors.New("missing id"))
	err.Hint = "run 'demo show --help'"
	code := FormatError(err, "json", &buf)
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	var env jsonErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if env.Error.Code != CodeUsage {
		t.Errorf("json code = %q, want %q", env.Error.Code, CodeUsage)
	}
	if env.Error.Message != "missing id" || env.Error.Hint != "run 'demo show --help'" {
		t.Errorf("json error = %+v", env.Error)
	}
}

func TestFormatError_YAML(t *testing.T) {
	var buf bytes.Buffer
	err := NewLatheError(CodeNotAuthenticated, ExitNotAuthenticated, errors.New("not authenticated"))
	err.Hint = "run 'demo auth login'"
	if code := FormatError(err, "yaml", &buf); code != ExitNotAuthenticated {
		t.Fatalf("exit = %d, want %d", code, ExitNotAuthenticated)
	}
	var env struct {
		Error LatheError `yaml:"error"`
	}
	if err := yaml.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, buf.String())
	}
	if env.Error.Code != CodeNotAuthenticated || env.Error.Hint != "run 'demo auth login'" {
		t.Fatalf("yaml error = %+v", env.Error)
	}
}

func TestFormatError_Plain(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(errors.New("oops"), "table", &buf)
	if code != ExitGeneral {
		t.Errorf("exit = %d, want %d", code, ExitGeneral)
	}
	if !strings.Contains(buf.String(), "oops") {
		t.Errorf("output missing error message: %q", buf.String())
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

func TestExecuteClassifiesFlagErrors(t *testing.T) {
	cmd := &cobra.Command{Use: "demo", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().StringP("output", "o", "table", "")
	cmd.Flags().Int("count", 0, "")
	cmd.SetArgs([]string{"-o", "json", "--count", "nope"})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	if code := Execute(cmd); code != ExitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, ExitUsage, stderr.String())
	}
	var env jsonErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON error: %v\n%s", err, stderr.String())
	}
	if env.Error.Code != CodeUsage || !strings.Contains(env.Error.Hint, "demo --help") {
		t.Fatalf("error = %+v", env.Error)
	}
}
