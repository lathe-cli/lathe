package lathe

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func mustBuild(t *testing.T, root *cobra.Command, service string, specs []runtime.CommandSpec) {
	t.Helper()
	if err := runtime.Build(root, service, specs); err != nil {
		t.Fatalf("Build(%q): %v", service, err)
	}
}

func TestCommandsJSON_EmptyCatalog(t *testing.T) {
	root := NewApp(testManifest())
	out, err := execute(root, "commands", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"commands": []`) {
		t.Fatalf("output missing empty commands array:\n%s", out)
	}
	var catalog runtime.Catalog
	if err := json.Unmarshal([]byte(out), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Commands == nil || len(catalog.Commands) != 0 {
		t.Fatalf("commands = %#v", catalog.Commands)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	var entry runtime.CatalogCommand
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatal(err)
	}
	if strings.Join(entry.Path, " ") != "demo users get-user" || entry.Group != "Users" {
		t.Fatalf("entry = %+v", entry)
	}
	if len(entry.Notes) != 1 || entry.Notes[0] != "Use the canonical user ID." {
		t.Fatalf("notes = %#v", entry.Notes)
	}
	if len(entry.Prerequisites) != 1 || entry.Prerequisites[0] != "List users before fetching details." {
		t.Fatalf("prerequisites = %#v", entry.Prerequisites)
	}
	if len(entry.KnownErrors) != 1 || entry.KnownErrors[0].Status != 400 || entry.KnownErrors[0].Cause != "missing id" {
		t.Fatalf("known errors = %#v", entry.KnownErrors)
	}
	if len(entry.Examples) != 1 || entry.Examples[0].Command != "myctl demo users get-user --id 123 -o json" || entry.Examples[0].OutputHints.IDPath != "data.user.id" {
		t.Fatalf("examples = %#v", entry.Examples)
	}
	if entry.Mutation != runtime.MutationRead {
		t.Fatalf("mutation = %q", entry.Mutation)
	}
	if entry.DryRun == nil || entry.DryRun.Mode != runtime.DryRunHTTPPreview || entry.DryRun.Flag != "dry-run" {
		t.Fatalf("dry_run = %+v", entry.DryRun)
	}
	if len(entry.Flags) != 2 || !entry.Flags[1].Required || entry.Flags[1].Name != "type" {
		t.Fatalf("required query flag = %#v", entry.Flags)
	}

	out, err = execute(root, "search", "getUser", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var results []runtime.SearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Command.Use != "get-user" {
		t.Fatalf("results = %+v", results)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	var entry runtime.CatalogCommand
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Body == nil || entry.Body.Template != tmpl || entry.Body.MergePath != "variables" {
		t.Fatalf("envelope body = %+v", entry.Body)
	}
	if entry.HTTP.Method != "POST" || entry.HTTP.PathTemplate != "/graphql" {
		t.Fatalf("http = %+v", entry.HTTP)
	}
	if entry.Mutation != runtime.MutationWrite {
		t.Fatalf("mutation = %q", entry.Mutation)
	}
	for _, want := range []string{`"template"`, `"merge_path"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	var entry runtime.CatalogCommand
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry.Flags) != 1 || entry.Flags[0].Name != "name" || entry.Flags[0].Location != runtime.InVariable || !entry.Flags[0].Required {
		t.Fatalf("variable flag = %+v", entry.Flags)
	}
	if entry.Body == nil || entry.Body.MergePath != "variables" {
		t.Fatalf("envelope body = %+v", entry.Body)
	}
}

func TestCommandsShow_NotFound(t *testing.T) {
	root := NewApp(testManifest())
	_, err := execute(root, "commands", "show", "demo", "users", "missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCommandsSchemaJSON(t *testing.T) {
	root := NewApp(testManifest())
	out, err := execute(root, "commands", "schema", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var schema runtime.CatalogSchema
	if err := json.Unmarshal([]byte(out), &schema); err != nil {
		t.Fatal(err)
	}
	want := runtime.CatalogSchemaDocument()
	if schema.CatalogSchemaVersion != want.CatalogSchemaVersion {
		t.Fatalf("schema = %d", schema.CatalogSchemaVersion)
	}
	if schema.DryRun.Result != want.DryRun.Result {
		t.Fatalf("dry_run = %+v", schema.DryRun)
	}
	if !strings.Contains(out, `"surfaces"`) || !strings.Contains(out, `"commands.show"`) {
		t.Fatalf("schema JSON missing surfaces:\n%s", out)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	var results []runtime.SearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

func testManifest() *config.Manifest {
	return &config.Manifest{CLI: config.CLIInfo{Name: "myctl", Short: "test cli", HostEnv: "MYCTL_HOST"}}
}

func execute(root *cobra.Command, args ...string) (string, error) {
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}
