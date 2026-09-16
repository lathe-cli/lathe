package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_ParameterFlagAliasesAndPositionalArgument(t *testing.T) {
	isolateRuntime(t)

	tests := []struct {
		name    string
		input   []string
		wantURL string
		wantErr string
	}{
		{name: "primary flag", input: []string{"--app-id", "a-1", "--workspace-id", "w-1"}, wantURL: "/apps/a-1?workspace_id=w-1"},
		{name: "legacy flag", input: []string{"--app_id", "a-1", "--workspace_id", "w-1"}, wantURL: "/apps/a-1?workspace_id=w-1"},
		{name: "positional", input: []string{"a-1", "--workspace-id", "w-1"}, wantURL: "/apps/a-1?workspace_id=w-1"},
		{name: "positional slice", input: []string{"a-1", "one,two", "--workspace-id", "w-1"}, wantURL: "/apps/a-1?tags=one&tags=two&workspace_id=w-1"},
		{name: "conflicting inputs", input: []string{"a-1", "--app-id", "a-2", "--workspace-id", "w-1"}, wantErr: "both argument 1 and --app-id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requestURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestURL = r.URL.String()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			root := newExecutionRoot("raw")
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			mustBuild(t, root, "demo", []CommandSpec{{
				Group: "Apps", Use: "describe", Method: "GET", PathTpl: "/apps/{app_id}",
				Params: []ParamSpec{{
					Name: "app_id", Flag: "app-id", Aliases: []string{"app_id"}, In: InPath,
					GoType: "string", Required: true, Argument: "id",
				}, {
					Name: "workspace_id", Flag: "workspace-id", Aliases: []string{"workspace_id"}, In: InQuery,
					GoType: "string", Required: true,
				}, {
					Name: "tags", Flag: "tags", In: InQuery, GoType: "[]string", Argument: "tags",
				}},
				Security: &SecurityHint{Public: true},
			}})
			describe := findChildCommand(mustFindChild(t, mustFindChild(t, root, "demo"), "apps"), "describe")
			if describe == nil {
				t.Fatal("describe command was not mounted")
				return
			}
			testutil.Require(t, describe.Use == "describe [id] [tags]", "use = %q, want describe [id] [tags]", describe.Use)
			args := []string{"--hostname", srv.URL, "demo", "apps", "describe"}
			root.SetArgs(append(args, tc.input...))

			err := root.Execute()
			if tc.wantErr != "" {
				testutil.Require(t, err != nil && strings.Contains(err.Error(), tc.wantErr), "Execute error = %v, want %q", err, tc.wantErr)
				testutil.Require(t, requestURL == "", "request sent to %q", requestURL)
				return
			}
			testutil.Require(t, err == nil, "Execute: %v", err)
			testutil.Require(t, requestURL == tc.wantURL, "URL = %q, want %q", requestURL, tc.wantURL)
		})
	}
}

func TestBuild_StdinInputsObserveCommandCancellation(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "body file from stdin", args: []string{"--file", "-"}},
		{name: "sensitive flag from stdin", args: []string{"--value-stdin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRuntime(t)
			useBlockedStdin(t)

			root := newRootWithModuleGroup()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.PersistentFlags().String("hostname", "", "")
			root.PersistentFlags().StringP("output", "o", "json", "")
			mustBuild(t, root, "demo", []CommandSpec{{
				Group:   "Users",
				Use:     "create-user",
				Method:  "POST",
				PathTpl: "/users",
				Params: []ParamSpec{
					{Name: "value", Flag: "value", In: InBody, GoType: "string", Format: "password"},
				},
				RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
			}})
			root.SetArgs(append([]string{"demo", "users", "create-user", "--hostname", "x.invalid", "--dry-run"}, tc.args...))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root.SetContext(ctx)
			time.AfterFunc(50*time.Millisecond, cancel)

			errs := make(chan error, 1)
			go func() { errs <- root.Execute() }()
			select {
			case err := <-errs:
				testutil.Require(t, errors.Is(err, context.Canceled), "Execute err = %v, want context.Canceled", err)
				if got := ClassifyError(err); got.Code != CodeCanceled || got.ExitCode != ExitCanceled {
					t.Fatalf("classified as %s/%d, want %s/%d", got.Code, got.ExitCode, CodeCanceled, ExitCanceled)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Execute kept blocking on stdin after the command context was canceled")
			}
		})
	}
}

func useBlockedStdin(t *testing.T) {
	t.Helper()
	r, w, err := os.Pipe()
	testutil.Require(t, err == nil, "%v", err)
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = w.Close()
		_ = r.Close()
	})
}

func TestInputError(t *testing.T) {
	cmd := &cobra.Command{Use: "demo"}
	canceled := fmt.Errorf("read stdin: %w", context.Canceled)
	if got := inputError(cmd, canceled); got != canceled {
		t.Fatalf("inputError(canceled) = %v, want the cancellation unchanged", got)
	}
	got := ClassifyError(inputError(cmd, errors.New("bad input")))
	testutil.Require(t, got.Code == CodeUsage && got.ExitCode == ExitUsage, "inputError(other) classified as %s/%d, want %s/%d", got.Code, got.ExitCode, CodeUsage, ExitUsage)
}

func TestBuild_RequiredQueryParamBlocksBeforeRequest(t *testing.T) {
	isolateRuntime(t)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Receivers",
		Use:     "get-receiver",
		Method:  "GET",
		PathTpl: "/receivers",
		Params: []ParamSpec{
			{Name: "type", Flag: "type", In: InQuery, GoType: "string", Required: true, Help: "Receiver type"},
		},
		Security: &SecurityHint{Public: true},
	}}

	root := newExecutionRoot("raw")

	mustBuild(t, root, "demo", specs)
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "receivers", "get-receiver"})

	err := root.Execute()
	testutil.Require(t, err != nil, "expected required flag error")
	testutil.Require(t, strings.Contains(err.Error(), "required flag") && strings.Contains(err.Error(), "type"), "unexpected error: %v", err)
	testutil.Require(t, hits == 0, "server hits = %d, want 0", hits)
}

func TestBuild_PaginationFlagsAttached(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Items",
		Use:     "list-items",
		Method:  "GET",
		PathTpl: "/items",
		Output: OutputHints{
			Pagination: &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token"},
		},
	}}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	items := mustFindChild(t, svc, "items")
	listItems := mustFindChild(t, items, "list-items")

	for _, name := range []string{"all", "max-pages"} {
		testutil.Check(t, listItems.Flag(name) != nil, "list-items missing --%s flag", name)
	}
}

func TestBuild_WaitFlagOnMutating(t *testing.T) {
	specs := []CommandSpec{
		{Group: "Resources", Use: "create-resource", Method: "POST", PathTpl: "/resources"},
		{Group: "Resources", Use: "list-resources", Method: "GET", PathTpl: "/resources"},
	}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	resources := mustFindChild(t, svc, "resources")

	create := mustFindChild(t, resources, "create-resource")
	testutil.Check(t, create.Flag("wait") != nil, "create-resource (POST) should have --wait flag")

	list := mustFindChild(t, resources, "list-resources")
	testutil.Check(t, list.Flag("wait") == nil, "list-resources (GET) should NOT have --wait flag")
}

func TestBuild_ControlFlagsAvoidOperationParameterCollisions(t *testing.T) {
	names := []string{"all", "max-pages", "wait", "dry-run", "file", "set", "set-str"}
	params := make([]ParamSpec, 0, len(names))
	for _, name := range names {
		params = append(params, ParamSpec{Name: name, Flag: name, In: InQuery, GoType: "string"})
	}
	params = append(params, ParamSpec{Name: "lathe-dry-run", Flag: "lathe-dry-run", In: InQuery, GoType: "string"})
	specs := []CommandSpec{{
		Group:       "Resources",
		Use:         "create-resource",
		Method:      "POST",
		PathTpl:     "/resources",
		Params:      params,
		RequestBody: &RequestBody{MediaType: "application/json"},
		Output: OutputHints{
			Pagination: &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token"},
		},
	}}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	create := mustFindChild(t, mustFindChild(t, mustFindChild(t, root, "demo"), "resources"), "create-resource")
	for _, name := range names {
		testutil.Check(t, create.Flag(name) != nil, "create-resource missing operation parameter --%s", name)
		testutil.Check(t, create.Flag("lathe-"+name) != nil, "create-resource missing collision-safe control flag --lathe-%s", name)
	}
	testutil.Check(t, create.Flag("lathe-dry-run-2") != nil, "create-resource missing deterministic fallback --lathe-dry-run-2")
}
