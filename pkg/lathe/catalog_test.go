package lathe

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func mustBuild(t *testing.T, root *cobra.Command, service string, specs []runtime.CommandSpec) {
	t.Helper()
	testutil.NoError(t, runtime.Build(root, service, specs))
}

func TestCommandsJSON_EmptyCatalog(t *testing.T) {
	root := NewApp(testManifest())
	out, err := execute(root, "commands", "--json")
	testutil.Require(t, err == nil, "%v", err)
	testutil.Require(t, strings.Contains(out, `"commands": []`), "output missing empty commands array:\n%s", out)
	var catalog runtime.Catalog
	testutil.NoError(t, json.Unmarshal([]byte(out), &catalog))
	testutil.Require(t, catalog.Commands != nil && len(catalog.Commands) == 0, "commands = %#v", catalog.Commands)
}

func TestCommandsShowAndSearchJSON(t *testing.T) {
	root := NewApp(testManifest())
	mustBuild(t, root, "demo", []runtime.CommandSpec{{
		Group: "Users",
		Use:   "get-user",
		Short: "Get a user",
		Examples: []runtime.CommandExample{{
			Summary:     "Get a user by ID",
			Command:     "myctl demo users get-user --id 123 -o json",
			OutputHints: &runtime.ExampleOutputHints{IDPath: "data.user.id"},
		}},
		OperationID: "getUser",
		Method:      "GET",
		PathTpl:     "/users/{id}",
		Params: []runtime.ParamSpec{
			{Name: "id", Flag: "id", In: runtime.InPath, GoType: "string", Required: true, Help: "User id"},
			{Name: "type", Flag: "type", In: runtime.InQuery, GoType: "string", Required: true, Help: "User type"},
		},
		Notes:         []string{"Use the canonical user ID."},
		Prerequisites: []string{"List users before fetching details."},
		KnownErrors:   []runtime.KnownError{{Status: 400, Cause: "missing id"}},
	}})

	out, err := execute(root, "commands", "show", "demo", "users", "get-user", "--json")
	testutil.Require(t, err == nil, "%v", err)
	var entry runtime.CatalogCommand
	testutil.NoError(t, json.Unmarshal([]byte(out), &entry))
	testutil.Require(t, strings.Join(entry.Path, " ") == "demo users get-user" && entry.Group == "Users", "entry = %+v", entry)
	testutil.Require(t, len(entry.Notes) == 1 && entry.Notes[0] == "Use the canonical user ID.", "notes = %#v", entry.Notes)
	testutil.Require(t, len(entry.Prerequisites) == 1 && entry.Prerequisites[0] == "List users before fetching details.", "prerequisites = %#v", entry.Prerequisites)
	testutil.Require(t, len(entry.KnownErrors) == 1 && entry.KnownErrors[0].Status == 400 && entry.KnownErrors[0].Cause == "missing id", "known errors = %#v", entry.KnownErrors)
	testutil.Require(t, len(entry.Examples) == 1 && entry.Examples[0].Command == "myctl demo users get-user --id 123 -o json" && entry.Examples[0].OutputHints.IDPath == "data.user.id", "examples = %#v", entry.Examples)
	testutil.Require(t, entry.Mutation == runtime.MutationRead, "mutation = %q", entry.Mutation)
	testutil.Require(t, entry.DryRun != nil && entry.DryRun.Mode == runtime.DryRunHTTPPreview && entry.DryRun.Flag == "dry-run", "dry_run = %+v", entry.DryRun)
	testutil.Require(t, len(entry.Flags) == 2 && entry.Flags[1].Required && entry.Flags[1].Name == "type", "required query flag = %#v", entry.Flags)

	out, err = execute(root, "search", "getUser", "--json")
	testutil.Require(t, err == nil, "%v", err)
	var results []runtime.SearchResult
	testutil.NoError(t, json.Unmarshal([]byte(out), &results))
	testutil.Require(t, len(results) == 1 && results[0].Command.Use == "get-user", "results = %+v", results)
}

func TestCommandsShow_EnvelopeBody(t *testing.T) {
	root := NewApp(testManifest())
	const tmpl = `{"query":"mutation CreateApp($name:String!){createApp(name:$name){id}}","variables":{}}`
	mustBuild(t, root, "demo", []runtime.CommandSpec{{
		Group:       "Apps",
		Use:         "create-app",
		Short:       "Create an app",
		OperationID: "Apps_CreateApp",
		Method:      "POST",
		PathTpl:     "/graphql",
		RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json", Template: tmpl, MergePath: "variables"},
	}})

	out, err := execute(root, "commands", "show", "demo", "apps", "create-app", "--json")
	testutil.Require(t, err == nil, "%v", err)
	var entry runtime.CatalogCommand
	testutil.NoError(t, json.Unmarshal([]byte(out), &entry))
	testutil.Require(t, entry.Body != nil && entry.Body.Template == tmpl && entry.Body.MergePath == "variables", "envelope body = %+v", entry.Body)
	testutil.Require(t, entry.HTTP.Method == "POST" && entry.HTTP.PathTemplate == "/graphql", "http = %+v", entry.HTTP)
	testutil.Require(t, entry.Mutation == runtime.MutationWrite, "mutation = %q", entry.Mutation)
	for _, want := range []string{`"template"`, `"merge_path"`} {
		testutil.Require(t, strings.Contains(out, want), "show output missing %q:\n%s", want, out)
	}
}

func TestCommandsShow_EnvelopeVariableFlag(t *testing.T) {
	root := NewApp(testManifest())
	const tmpl = `{"query":"mutation createApp($name: String!){createApp(name:$name){id}}","variables":{}}`
	mustBuild(t, root, "demo", []runtime.CommandSpec{{
		Group:       "Apps",
		Use:         "create-app",
		Short:       "Create an app",
		OperationID: "Apps_CreateApp",
		Method:      "POST",
		PathTpl:     "/graphql",
		Params: []runtime.ParamSpec{
			{Name: "name", Flag: "name", In: runtime.InVariable, GoType: "string", Required: true, Help: "app name"},
		},
		RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json", Template: tmpl, MergePath: "variables"},
	}})

	out, err := execute(root, "commands", "show", "demo", "apps", "create-app", "--json")
	testutil.Require(t, err == nil, "%v", err)
	var entry runtime.CatalogCommand
	testutil.NoError(t, json.Unmarshal([]byte(out), &entry))
	testutil.Require(t, len(entry.Flags) == 1 && entry.Flags[0].Name == "name" && entry.Flags[0].Location == runtime.InVariable && entry.Flags[0].Required, "variable flag = %+v", entry.Flags)
	testutil.Require(t, entry.Body != nil && entry.Body.MergePath == "variables", "envelope body = %+v", entry.Body)
}

func TestCommandsShow_NotFound(t *testing.T) {
	root := NewApp(testManifest())
	_, err := execute(root, "commands", "show", "demo", "users", "missing")
	testutil.Require(t, err != nil, "expected error")
}

func TestCommandsSchemaJSON(t *testing.T) {
	root := NewApp(testManifest())
	out, err := execute(root, "commands", "schema", "--json")
	testutil.Require(t, err == nil, "%v", err)
	var schema runtime.CatalogSchema
	testutil.NoError(t, json.Unmarshal([]byte(out), &schema))
	want := runtime.CatalogSchemaDocument()
	testutil.Require(t, schema.CatalogSchemaVersion == want.CatalogSchemaVersion, "schema = %d", schema.CatalogSchemaVersion)
	testutil.Require(t, schema.DryRun.Result == want.DryRun.Result, "dry_run = %+v", schema.DryRun)
	testutil.Require(t, strings.Contains(out, `"surfaces"`) && strings.Contains(out, `"commands.show"`), "schema JSON missing surfaces:\n%s", out)
}

func TestSearchExcludesHiddenCommands(t *testing.T) {
	root := NewApp(testManifest())
	mustBuild(t, root, "demo", []runtime.CommandSpec{{
		Group:   "Users",
		Use:     "delete-user",
		Short:   "Delete a user",
		Method:  "DELETE",
		PathTpl: "/users/{id}",
		Hidden:  true,
	}})

	out, err := execute(root, "search", "delete", "--json")
	testutil.Require(t, err == nil, "%v", err)
	var results []runtime.SearchResult
	testutil.NoError(t, json.Unmarshal([]byte(out), &results))
	testutil.Require(t, len(results) == 0, "results = %+v", results)
}

func testManifest() *config.Manifest {
	return &config.Manifest{CLI: config.CLIInfo{Name: "myctl", Short: "test cli", HostEnv: "MYCTL_HOST"}}
}

func TestCommandsShowUnknownPathUsageError(t *testing.T) {
	root := NewApp(testManifest())
	_, err := execute(root, "commands", "show", "keys", "nope-secret")
	var le *runtime.LatheError
	testutil.Require(t, errors.As(err, &le), "expected LatheError, got %v", err)
	testutil.Require(t, le.Code == runtime.CodeUsage, "code = %q, want %q", le.Code, runtime.CodeUsage)
	testutil.Require(t, strings.Contains(le.Detail, "no generated command"), "detail = %q", le.Detail)
	testutil.Require(t, strings.Contains(le.Detail, "myctl commands"), "detail missing listing hint: %q", le.Detail)
	testutil.Require(t, !strings.Contains(le.Detail, "nope-secret"), "detail echoed user input: %q", le.Detail)
}

func TestSearchEmptyQueryUsageError(t *testing.T) {
	root := NewApp(testManifest())
	_, err := execute(root, "search", "  ")
	var le *runtime.LatheError
	testutil.Require(t, errors.As(err, &le), "expected LatheError, got %v", err)
	testutil.Require(t, le.Code == runtime.CodeUsage, "code = %q, want %q", le.Code, runtime.CodeUsage)
	testutil.Require(t, le.Detail == "search query must not be empty", "detail = %q", le.Detail)
}

func execute(root *cobra.Command, args ...string) (string, error) {
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}
