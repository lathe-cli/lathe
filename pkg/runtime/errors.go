package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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

type ErrorHTTPContext struct {
	Status int `json:"status" yaml:"status"`
}

type LatheError struct {
	Code     string            `json:"code" yaml:"code"`
	Message  string            `json:"message" yaml:"message"`
	Hint     string            `json:"hint" yaml:"hint"`
	HTTP     *ErrorHTTPContext `json:"http,omitempty" yaml:"http,omitempty"`
	ExitCode int               `json:"-" yaml:"-"`
	cause    error
}

func (e *LatheError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return e.Message
}

func (e *LatheError) Unwrap() error {
	return e.cause
}

func NewLatheError(code string, exitCode int, cause error) *LatheError {
	return NewError(code, exitCode, cause.Error(), defaultErrorHint(code), cause)
}

func NewError(code string, exitCode int, message, hint string, cause error) *LatheError {
	return &LatheError{
		Code:     code,
		Message:  message,
		Hint:     hint,
		ExitCode: exitCode,
		cause:    cause,
	}
}

func UsageError(cmd *cobra.Command, cause error) *LatheError {
	hint := "run the command with --help and correct the arguments"
	if cmd != nil && cmd.CommandPath() != "" {
		hint = fmt.Sprintf("run `%s --help` and correct the arguments", cmd.CommandPath())
	}
	return NewError(CodeUsage, ExitUsage, "invalid command usage", hint, cause)
}

func UsageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return UsageError(cmd, err)
		}
		return nil
	}
}

func newAPIError(cause error, status int) *LatheError {
	hint := "check the hostname, network connectivity, and TLS settings"
	if status != 0 {
		hint = "check the request and API documentation"
		switch {
		case status == 401 || status == 403:
			hint = "check authentication and authorization"
		case status == 429 || status >= 500:
			hint = "retry later or contact the API operator"
		}
	}
	err := NewError(CodeAPIError, ExitAPIError, "API request failed", hint, cause)
	if status != 0 {
		err.HTTP = &ErrorHTTPContext{Status: status}
	}
	return err
}

func ClassifyError(err error) *LatheError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return NewError(CodeCanceled, ExitCanceled, "command canceled", "retry the command when ready", err)
	}
	var le *LatheError
	if errors.As(err, &le) {
		if le.Hint == "" {
			le.Hint = defaultErrorHint(le.Code)
		}
		return le
	}
	if errors.Is(err, ErrNotAuthenticated) {
		return NewError(CodeNotAuthenticated, ExitNotAuthenticated, "authentication required", "run `auth login` for the selected hostname", err)
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return newAPIError(err, he.Status)
	}
	return NewError(CodeGeneral, ExitGeneral, "command failed", "check local configuration and retry", err)
}

func defaultErrorHint(code string) string {
	switch code {
	case CodeUsage:
		return "run the command with --help and correct the arguments"
	case CodeAPIError:
		return "check the request and API documentation"
	case CodeNotAuthenticated:
		return "run `auth login` for the selected hostname"
	case CodeCanceled:
		return "retry the command when ready"
	default:
		return "check local configuration and retry"
	}
}

type errorEnvelope struct {
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
		_ = enc.Encode(errorEnvelope{Error: *le})
	case "yaml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		_ = enc.Encode(errorEnvelope{Error: *le})
		_ = enc.Close()
	default:
		fmt.Fprintln(w, "Error:", le.Message)
		fmt.Fprintln(w, "Hint:", le.Hint)
	}
	return le.ExitCode
}

func Execute(cmd *cobra.Command) int {
	cmd.SilenceErrors = true
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
