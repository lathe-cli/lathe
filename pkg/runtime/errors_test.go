package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
	err := &HTTPError{Method: "GET", URL: "/x", Status: 500, Body: []byte("fail")}
	le := ClassifyError(err)
	if le.Code != CodeAPIError {
		t.Errorf("code = %q, want %q", le.Code, CodeAPIError)
	}
	if le.ExitCode != ExitAPIError {
		t.Errorf("exit = %d, want %d", le.ExitCode, ExitAPIError)
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
	code := FormatError(errors.New("oops"), "json", &buf)
	if code != ExitGeneral {
		t.Errorf("exit = %d, want %d", code, ExitGeneral)
	}
	var env jsonErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if env.Error.Code != CodeGeneral {
		t.Errorf("json code = %q, want %q", env.Error.Code, CodeGeneral)
	}
	if env.Error.Message != "oops" {
		t.Errorf("json message = %q, want %q", env.Error.Message, "oops")
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
