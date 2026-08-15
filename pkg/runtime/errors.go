package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	ExitOK               = 0
	ExitGeneral          = 1
	ExitUsage            = 2
	ExitAPIError         = 3
	ExitNotAuthenticated = 4
	ExitCanceled         = 130
)

const (
	CodeGeneral          = "general"
	CodeUsage            = "usage"
	CodeAPIError         = "api_error"
	CodeNotAuthenticated = "not_authenticated"
	CodeCanceled         = "canceled"
)

type LatheError struct {
	Code       string `json:"code" yaml:"code"`
	Message    string `json:"message" yaml:"message"`
	Hint       string `json:"hint,omitempty" yaml:"hint,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty" yaml:"http_status,omitempty"`
	Method     string `json:"method,omitempty" yaml:"method,omitempty"`
	URL        string `json:"url,omitempty" yaml:"url,omitempty"`
	ServerBody string `json:"server_body,omitempty" yaml:"server_body,omitempty"`
	ExitCode   int    `json:"-" yaml:"-"`
	cause      error
}

func (e *LatheError) Error() string {
	return e.Message
}

func (e *LatheError) Unwrap() error {
	return e.cause
}

func NewLatheError(code string, exitCode int, cause error) *LatheError {
	return &LatheError{
		Code:     code,
		Message:  cause.Error(),
		ExitCode: exitCode,
		cause:    cause,
	}
}

func ClassifyError(err error) *LatheError {
	if err == nil {
		return nil
	}
	var le *LatheError
	if errors.As(err, &le) {
		return le
	}
	if errors.Is(err, context.Canceled) {
		return &LatheError{
			Code: CodeCanceled, Message: "operation canceled", ExitCode: ExitCanceled, cause: err,
		}
	}
	if errors.Is(err, ErrNotAuthenticated) {
		return NewLatheError(CodeNotAuthenticated, ExitNotAuthenticated, err)
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return &LatheError{
			Code:       CodeAPIError,
			Message:    fmt.Sprintf("request failed with HTTP %d", he.Status),
			Hint:       "run again with --debug to inspect a redacted server response",
			HTTPStatus: he.Status,
			Method:     he.Method,
			URL:        he.URL,
			ServerBody: safeHTTPServerBody(he.Body),
			ExitCode:   ExitAPIError,
			cause:      err,
		}
	}
	return NewLatheError(CodeGeneral, ExitGeneral, err)
}

type jsonErrorEnvelope struct {
	Error LatheError `json:"error" yaml:"error"`
}

type silentExitError interface {
	SilentExitCode() int
}

func FormatError(err error, format string, w io.Writer) int {
	le := ClassifyError(err)
	if le == nil {
		return ExitOK
	}
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(jsonErrorEnvelope{Error: *le})
	case "yaml", "yml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		_ = enc.Encode(jsonErrorEnvelope{Error: *le})
	default:
		fmt.Fprintln(w, "Error:", le.Message)
		if le.Hint != "" {
			fmt.Fprintln(w, "hint:", le.Hint)
		}
		if le.Method != "" && le.URL != "" {
			fmt.Fprintf(w, "request: %s %s\n", le.Method, le.URL)
		}
		if le.HTTPStatus != 0 {
			fmt.Fprintln(w, "http_status:", le.HTTPStatus)
		}
	}
	return le.ExitCode
}

func Execute(cmd *cobra.Command) int {
	cmd.SilenceErrors = true
	cmd.SetFlagErrorFunc(func(active *cobra.Command, err error) error {
		return newUsageError(active, err)
	})
	err := cmd.Execute()
	if err == nil {
		return ExitOK
	}
	var silent silentExitError
	if errors.As(err, &silent) {
		return silent.SilentExitCode()
	}
	format, _ := cmd.PersistentFlags().GetString("output")
	return FormatError(err, format, cmd.ErrOrStderr())
}

func newUsageError(cmd *cobra.Command, cause error) *LatheError {
	path := strings.TrimSpace(cmd.CommandPath())
	if path == "" {
		path = cmd.Name()
	}
	err := NewLatheError(CodeUsage, ExitUsage, cause)
	err.Hint = fmt.Sprintf("run '%s --help'", path)
	return err
}

func safeHTTPServerBody(body []byte) string {
	if !json.Valid(body) {
		return ""
	}
	const maxRunes = 1024
	redacted := []rune(strings.TrimSpace(string(redactDebugBody("application/json", body))))
	if len(redacted) > maxRunes {
		return string(redacted[:maxRunes]) + "…"
	}
	return string(redacted)
}
