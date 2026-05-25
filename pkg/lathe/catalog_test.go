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
	runtime.Build(root, "demo", []runtime.CommandSpec{{
		Group:       "Users",
		Use:         "get-user",
		Short:       "Get a user",
		OperationID: "getUser",
		Method:      "GET",
		PathTpl:     "/users/{id}",
		Params: []runtime.ParamSpec{
			{Name: "id", Flag: "id", In: runtime.InPath, GoType: "string", Required: true, Help: "User id"},
		},
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
	var data map[string]int
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatal(err)
	}
	if data["catalog_schema_version"] != runtime.CatalogSchemaVersion {
		t.Fatalf("schema = %d", data["catalog_schema_version"])
	}
}

func TestSearchExcludesHiddenCommands(t *testing.T) {
	root := NewApp(testManifest())
	runtime.Build(root, "demo", []runtime.CommandSpec{{
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
