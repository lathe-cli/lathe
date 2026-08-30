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

func TestUsageErrorLiftsSafeDetail(t *testing.T) {
	cause := fmt.Errorf("invalid value %q for --range: must be one of 7, 30", "14")
	le := UsageError(nil, WithUsageDetail(cause, "--range accepts: 7, 30"))
	if le.Detail != "--range accepts: 7, 30" {
		t.Fatalf("detail = %q", le.Detail)
	}
	wrapped := UsageError(nil, fmt.Errorf("outer: %w", WithUsageDetail(cause, "--range accepts: 7, 30")))
	if wrapped.Detail != "--range accepts: 7, 30" {
		t.Fatalf("wrapped detail = %q", wrapped.Detail)
	}
	if plain := UsageError(nil, cause); plain.Detail != "" {
		t.Fatalf("plain detail = %q, want empty", plain.Detail)
	}
}

func TestWithUsageDetailSanitizesAndBounds(t *testing.T) {
	le := UsageError(nil, WithUsageDetail(errors.New("x"), "a\nb\tc\x07d  e"))
	if le.Detail != "a b c d e" {
		t.Fatalf("sanitized detail = %q", le.Detail)
	}
	hostile := UsageError(nil, WithUsageDetail(errors.New("x"), "a\u009b31mb c\u009d0;d e\u202ef"))
	if hostile.Detail != "a 31mb c 0;d e f" {
		t.Fatalf("hostile detail = %q, C1/bidi runes must be stripped", hostile.Detail)
	}
	long := strings.Repeat("v", 500)
	bounded := UsageError(nil, WithUsageDetail(errors.New("x"), long))
	if n := len([]rune(bounded.Detail)); n > 240 {
		t.Fatalf("detail rune length = %d, want <= 240", n)
	}
	if !strings.HasSuffix(bounded.Detail, "…") {
		t.Fatalf("bounded detail missing truncation marker: %q", bounded.Detail)
	}
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
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if env.Error.Detail != "missing required: name" {
		t.Fatalf("json detail = %q", env.Error.Detail)
	}

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
			if le.Message != "API request failed" {
				t.Fatalf("message = %q", le.Message)
			}
			if le.Detail != tc.want {
				t.Fatalf("detail = %q, want %q", le.Detail, tc.want)
			}
		})
	}
}

func TestClassifyError_ServerMessageSanitizedAndBounded(t *testing.T) {
	msg := "line1\nline2\t\x07\u009b31m\u009d0;\u202e" + strings.Repeat("x", 400)
	body, err := json.Marshal(map[string]string{"message": msg})
	if err != nil {
		t.Fatal(err)
	}
	he := &HTTPError{Status: 500, ContentType: "application/json", Body: body}
	le := ClassifyError(he)
	if strings.ContainsAny(le.Detail, "\n\t\x07\u009b\u009d\u202e") {
		t.Fatalf("detail not sanitized: %q", le.Detail)
	}
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
