package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

const (
	ExitOK               = 0
	ExitGeneral          = 1
	ExitUsage            = 2
	ExitAPIError         = 3
	ExitNotAuthenticated = 4
)

const (
	CodeGeneral          = "general"
	CodeUsage            = "usage"
	CodeAPIError         = "api_error"
	CodeNotAuthenticated = "not_authenticated"
)

type LatheError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ExitCode int    `json:"-"`
	cause    error
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
	if errors.Is(err, ErrNotAuthenticated) {
		return NewLatheError(CodeNotAuthenticated, ExitNotAuthenticated, err)
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return NewLatheError(CodeAPIError, ExitAPIError, err)
	}
	return NewLatheError(CodeGeneral, ExitGeneral, err)
}

type jsonErrorEnvelope struct {
	Error LatheError `json:"error"`
}

type silentExitError interface {
	SilentExitCode() int
}

func FormatError(err error, format string, w io.Writer) int {
	le := ClassifyError(err)
	if le == nil {
		return ExitOK
	}
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(jsonErrorEnvelope{Error: *le})
	} else {
		fmt.Fprintln(w, "Error:", le.Message)
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
	return FormatError(err, format, os.Stderr)
}
