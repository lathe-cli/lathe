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

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestClassifyError_Nil(t *testing.T) {
	testutil.Require(t, ClassifyError(nil) == nil, "expected nil for nil error")
}

func TestClassifyError_NotAuthenticated(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", ErrNotAuthenticated)
	le := ClassifyError(err)
	testutil.Check(t, le.Code == CodeNotAuthenticated, "code = %q, want %q", le.Code, CodeNotAuthenticated)
	testutil.Check(t, le.ExitCode == ExitNotAuthenticated, "exit = %d, want %d", le.ExitCode, ExitNotAuthenticated)
	testutil.Require(t, le.Hint != "", "authentication error missing hint")
}

func TestClassifyError_HTTPError(t *testing.T) {
	err := &HTTPError{Method: "GET", URL: "/private", Status: 500, Body: []byte("upstream-secret")}
	le := ClassifyError(err)
	testutil.Check(t, le.Code == CodeAPIError, "code = %q, want %q", le.Code, CodeAPIError)
	testutil.Check(t, le.ExitCode == ExitAPIError, "exit = %d, want %d", le.ExitCode, ExitAPIError)
	testutil.Require(t, le.HTTP != nil && le.HTTP.Status == 500, "http context = %#v, want status 500", le.HTTP)
	testutil.Require(t, !strings.Contains(le.Message, "upstream-secret") && !strings.Contains(le.Message, "/private"), "machine message exposed HTTP details: %q", le.Message)
}

func TestClassifyError_Passthrough(t *testing.T) {
	orig := NewLatheError(CodeUsage, ExitUsage, errors.New("bad flag"))
	le := ClassifyError(orig)
	testutil.Check(t, le == orig, "expected same LatheError instance returned")
}

func TestClassifyError_Generic(t *testing.T) {
	le := ClassifyError(errors.New("boom"))
	testutil.Check(t, le.Code == CodeGeneral, "code = %q, want %q", le.Code, CodeGeneral)
	testutil.Check(t, le.ExitCode == ExitGeneral, "exit = %d, want %d", le.ExitCode, ExitGeneral)
	testutil.Require(t, le.Message == "command failed" && le.Hint != "", "generic machine error = %#v", le)
}

func TestFormatError_JSON(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(errors.New("oops"), "json", &buf)
	testutil.Check(t, code == ExitGeneral, "exit = %d, want %d", code, ExitGeneral)
	var env errorEnvelope
	testutil.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	testutil.Check(t, env.Error.Code == CodeGeneral, "json code = %q, want %q", env.Error.Code, CodeGeneral)
	testutil.Check(t, env.Error.Message == "command failed" && env.Error.Hint != "", "json error = %#v", env.Error)
}

func TestFormatError_YAML(t *testing.T) {
	var buf bytes.Buffer
	cause := &HTTPError{Method: "POST", URL: "https://example.invalid/private", Status: 429, Body: []byte("upstream-secret")}
	if code := FormatError(cause, "yaml", &buf); code != ExitAPIError {
		t.Fatalf("exit = %d, want %d", code, ExitAPIError)
	}
	var env errorEnvelope
	testutil.NoError(t, yaml.Unmarshal(buf.Bytes(), &env))
	testutil.Require(t, env.Error.Code == CodeAPIError && env.Error.Hint != "" && env.Error.HTTP != nil && env.Error.HTTP.Status == 429, "yaml error = %#v", env.Error)
	if strings.Contains(buf.String(), "upstream-secret") || strings.Contains(buf.String(), "private") {
		t.Fatalf("YAML leaked HTTP details: %s", buf.String())
	}
}

func TestFormatError_Plain(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(errors.New("oops"), "table", &buf)
	testutil.Check(t, code == ExitGeneral, "exit = %d, want %d", code, ExitGeneral)
	if !strings.Contains(buf.String(), "Error: command failed") || !strings.Contains(buf.String(), "Hint:") || strings.Contains(buf.String(), "oops") {
		t.Errorf("plain error = %q", buf.String())
	}
}

func TestFormatError_Nil(t *testing.T) {
	var buf bytes.Buffer
	code := FormatError(nil, "json", &buf)
	testutil.Check(t, code == ExitOK, "exit = %d, want %d", code, ExitOK)
	if buf.Len() != 0 {
		t.Errorf("expected empty output for nil error, got %q", buf.String())
	}
}

func TestLatheError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	le := NewLatheError(CodeGeneral, ExitGeneral, cause)
	testutil.Check(t, errors.Is(le, cause), "expected Unwrap to expose cause")
}

func TestClassifyError_Canceled(t *testing.T) {
	le := ClassifyError(fmt.Errorf("request: %w", context.Canceled))
	testutil.Require(t, le.Code == CodeCanceled && le.ExitCode == ExitCanceled && le.Hint != "", "canceled error = %#v", le)
}

func TestUsageErrorLiftsSafeDetail(t *testing.T) {
	cause := fmt.Errorf("invalid value %q for --range: must be one of 7, 30", "14")
	le := UsageError(nil, WithUsageDetail(cause, "--range accepts: 7, 30"))
	testutil.Require(t, le.Detail == "--range accepts: 7, 30", "detail = %q", le.Detail)
	wrapped := UsageError(nil, fmt.Errorf("outer: %w", WithUsageDetail(cause, "--range accepts: 7, 30")))
	testutil.Require(t, wrapped.Detail == "--range accepts: 7, 30", "wrapped detail = %q", wrapped.Detail)
	if plain := UsageError(nil, cause); plain.Detail != "" {
		t.Fatalf("plain detail = %q, want empty", plain.Detail)
	}
}

func TestWithUsageDetailSanitizesAndBounds(t *testing.T) {
	le := UsageError(nil, WithUsageDetail(errors.New("x"), "a\nb\tc\x07d  e"))
	testutil.Require(t, le.Detail == "a b c d e", "sanitized detail = %q", le.Detail)
	hostile := UsageError(nil, WithUsageDetail(errors.New("x"), "a\u009b31mb c\u009d0;d e\u202ef"))
	testutil.Require(t, hostile.Detail == "a 31mb c 0;d e f", "hostile detail = %q, C1/bidi runes must be stripped", hostile.Detail)
	long := strings.Repeat("v", 500)
	bounded := UsageError(nil, WithUsageDetail(errors.New("x"), long))
	if n := len([]rune(bounded.Detail)); n > 240 {
		t.Fatalf("detail rune length = %d, want <= 240", n)
	}
	testutil.Require(t, strings.HasSuffix(bounded.Detail, "…"), "bounded detail missing truncation marker: %q", bounded.Detail)
}

func TestFormatError_PlainIncludesDetail(t *testing.T) {
	var buf bytes.Buffer
	le := UsageError(nil, WithUsageDetail(errors.New("boom"), "--range accepts: 7, 30"))
	if code := FormatError(le, "table", &buf); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	want := "Error: invalid command usage\nDetail: --range accepts: 7, 30\nHint: run the command with --help and correct the arguments\n"
	if buf.String() != want {
		t.Fatalf("plain output = %q, want %q", buf.String(), want)
	}
}

func TestFormatError_JSONDetail(t *testing.T) {
	var buf bytes.Buffer
	le := UsageError(nil, WithUsageDetail(errors.New("boom"), "missing required: name"))
	if code := FormatError(le, "json", &buf); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	var env errorEnvelope
	testutil.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	testutil.Require(t, env.Error.Detail == "missing required: name", "json detail = %q", env.Error.Detail)

	buf.Reset()
	if code := FormatError(errors.New("oops"), "json", &buf); code != ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, ExitGeneral)
	}
	if strings.Contains(buf.String(), "detail") {
		t.Fatalf("empty detail must be omitted from envelope: %s", buf.String())
	}
}

func TestClassifyError_DeclaredServerMessageDetail(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{"message field", "application/json", `{"message":"api key revoked"}`, "api key revoked"},
		{"message wins over error", "application/json", `{"message":"m","error":"e"}`, "m"},
		{"error string", "application/json", `{"error":"quota exceeded"}`, "quota exceeded"},
		{"nested error message", "application/json; charset=utf-8", `{"error":{"message":"nested cause"}}`, "nested cause"},
		{"detail field", "application/problem+json", `{"detail":"missing scope"}`, "missing scope"},
		{"non-json content type", "text/plain", `{"message":"nope"}`, ""},
		{"missing content type", "", `{"message":"nope"}`, ""},
		{"invalid json", "application/json", `{"message":`, ""},
		{"non-string message", "application/json", `{"message":{"deep":"x"}}`, ""},
		{"blank message", "application/json", `{"message":"   "}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			he := &HTTPError{Method: "GET", URL: "/private", Status: 403, ContentType: tc.contentType, Body: []byte(tc.body)}
			le := ClassifyError(he)
			testutil.Require(t, le.Message == "API request failed", "message = %q", le.Message)
			testutil.Require(t, le.Detail == tc.want, "detail = %q, want %q", le.Detail, tc.want)
		})
	}
}

func TestClassifyError_ServerMessageSanitizedAndBounded(t *testing.T) {
	msg := "line1\nline2\t\x07\u009b31m\u009d0;\u202e" + strings.Repeat("x", 400)
	body, err := json.Marshal(map[string]string{"message": msg})
	testutil.Require(t, err == nil, "%v", err)
	he := &HTTPError{Status: 500, ContentType: "application/json", Body: body}
	le := ClassifyError(he)
	testutil.Require(t, !strings.ContainsAny(le.Detail, "\n\t\x07\u009b\u009d\u202e"), "detail not sanitized: %q", le.Detail)
	if n := len([]rune(le.Detail)); n > 240 {
		t.Fatalf("detail rune length = %d, want <= 240", n)
	}
}

func TestClassifyError_OversizedErrorBodyIgnored(t *testing.T) {
	body := []byte(`{"message":"` + strings.Repeat("a", 33*1024) + `"}`)
	he := &HTTPError{Status: 500, ContentType: "application/json", Body: body}
	if le := ClassifyError(he); le.Detail != "" {
		t.Fatalf("oversized body must not produce detail, got %q", le.Detail)
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
