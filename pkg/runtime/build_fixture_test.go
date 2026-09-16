package runtime

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func newRootWithModuleGroup() *cobra.Command {
	root := &cobra.Command{Use: "myctl"}
	root.AddGroup(&cobra.Group{ID: ModuleGroupID, Title: "Modules"})
	return root
}

func mustBuild(t *testing.T, root *cobra.Command, service string, specs []CommandSpec) {
	t.Helper()
	testutil.NoError(t, Build(root, service, specs))
}

func newRecordingRoot(t *testing.T, spec CommandSpec) (*cobra.Command, string, func() ([]byte, bool)) {
	t.Helper()
	isolateRuntime(t)

	var rawBody []byte
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var err error
		rawBody, err = io.ReadAll(r.Body)
		testutil.Check(t, err == nil, "read request body: %v", err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	root := newExecutionRoot("raw")

	mustBuild(t, root, "demo", []CommandSpec{spec})
	return root, srv.URL, func() ([]byte, bool) { return rawBody, called }
}

func mustFindChild(t *testing.T, parent *cobra.Command, use string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Use == use {
			return c
		}
	}
	t.Fatalf("%s has no child %q; children = %v", parent.Use, use, cmdNames(parent.Commands()))
	return nil
}

func cmdNames(cmds []*cobra.Command) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Use)
	}
	return names
}

func isRequiredFlag(annotations map[string][]string) bool {
	for _, v := range annotations[cobra.BashCompOneRequiredFlag] {
		if v == "true" {
			return true
		}
	}
	return false
}

func isolateRuntime(t *testing.T) {
	t.Helper()
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())
}

func newExecutionRoot(format string) *cobra.Command {
	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", format, "")
	return root
}

func executeCommandError(t *testing.T, specs []CommandSpec, args ...string) *LatheError {
	t.Helper()
	isolateRuntime(t)
	root := newExecutionRoot("table")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)
	root.SetArgs(args)
	err := root.Execute()
	var le *LatheError
	testutil.Require(t, errors.As(err, &le), "expected LatheError, got %v", err)
	return le
}

func newWorkflowRoot(out io.Writer) *cobra.Command {
	root := newExecutionRoot("raw")
	root.SetOut(out)
	root.SetErr(io.Discard)
	return root
}

func publicGetSpec(use string, path string) CommandSpec {
	return CommandSpec{
		Group:    "System",
		Use:      use,
		Method:   "GET",
		PathTpl:  path,
		Security: &SecurityHint{Public: true},
	}
}

func decodeWorkflowSummary(t *testing.T, raw string) WorkflowResult {
	t.Helper()
	var summary WorkflowResult
	testutil.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(raw)), &summary))
	return summary
}

func stepStatuses(result WorkflowResult) map[string]string {
	out := map[string]string{}
	for _, step := range result.Steps {
		out[step.ID] = step.Status
	}
	return out
}

func workflowServer(t *testing.T, paths *[]string, response string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv
}
