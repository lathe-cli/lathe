package runtime

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildCatalog_UsesAttachedSpec(t *testing.T) {
	root := newRootWithModuleGroup()
	Build(root, "demo", []CommandSpec{
		{
			Group:       "Users",
			Use:         "get-user",
			Aliases:     []string{"show-user"},
			Short:       "Get a user",
			Long:        "Get one user by id.",
			OperationID: "getUser",
			Method:      "GET",
			PathTpl:     "/users/{id}",
			Params: []ParamSpec{
				{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: "User id"},
				{Name: "workspace", Flag: "workspace", In: InQuery, GoType: "string", Default: "default", Enum: []string{"default", "prod"}, Format: "slug", Help: "Target workspace"},
			},
			RequestBody: &RequestBody{Required: true, MediaType: "application/json", Schema: &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{"name": {Type: "string"}}}},
			Output: OutputHints{
				ListPath:          "data.items",
				DefaultColumns:    []string{"id", "name"},
				ResponseMediaType: "application/json",
				Pagination:        &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token", LimitParam: "limit"},
				Streaming:         &StreamingHint{Strategy: "sse"},
			},
			Security:      &SecurityHint{Scopes: []string{"users:read"}},
			Notes:         []string{"Use the canonical user ID."},
			Prerequisites: []string{"List users before fetching details."},
			KnownErrors:   []KnownError{{Status: 400, Cause: "missing id"}},
		},
	})

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl", CLIVersion: "v1.2.3"})
	if catalog.CatalogSchemaVersion != CatalogSchemaVersion {
		t.Fatalf("schema = %d, want %d", catalog.CatalogSchemaVersion, CatalogSchemaVersion)
	}
	if catalog.CLI.Name != "myctl" || catalog.CLI.Version != "v1.2.3" {
		t.Fatalf("cli = %+v", catalog.CLI)
	}
	if len(catalog.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(catalog.Commands))
	}

	cmd := catalog.Commands[0]
	if !reflect.DeepEqual(cmd.Path, []string{"demo", "users", "get-user"}) {
		t.Fatalf("path = %#v", cmd.Path)
	}
	if cmd.Group != "Users" {
		t.Fatalf("group = %q, want original casing", cmd.Group)
	}
	if cmd.Service != "demo" || cmd.Use != "get-user" || cmd.OperationID != "getUser" {
		t.Fatalf("command identity = %+v", cmd)
	}
	if cmd.Auth.Required != true || !reflect.DeepEqual(cmd.Auth.Scopes, []string{"users:read"}) {
		t.Fatalf("auth = %+v", cmd.Auth)
	}
	if cmd.Body == nil || !cmd.Body.Required || cmd.Body.MediaType != "application/json" {
		t.Fatalf("body = %+v", cmd.Body)
	}
	if cmd.Body.Schema == nil || cmd.Body.Schema.Properties["name"].Type != "string" {
		t.Fatalf("body schema = %+v", cmd.Body.Schema)
	}
	if len(cmd.Flags) != 2 {
		t.Fatalf("flags = %d, want 2", len(cmd.Flags))
	}
	if cmd.Flags[0].Location != InPath || !cmd.Flags[0].Required {
		t.Fatalf("path flag = %+v", cmd.Flags[0])
	}
	if cmd.Flags[1].Default != "default" || !reflect.DeepEqual(cmd.Flags[1].Enum, []string{"default", "prod"}) {
		t.Fatalf("query flag = %+v", cmd.Flags[1])
	}
	if cmd.Output.Pagination == nil || cmd.Output.Pagination.TokenParam != "page_token" {
		t.Fatalf("pagination = %+v", cmd.Output.Pagination)
	}
	if cmd.Output.Streaming == nil || cmd.Output.Streaming.Strategy != "sse" {
		t.Fatalf("streaming = %+v", cmd.Output.Streaming)
	}
	if !reflect.DeepEqual(cmd.Notes, []string{"Use the canonical user ID."}) {
		t.Fatalf("notes = %#v", cmd.Notes)
	}
	if !reflect.DeepEqual(cmd.Prerequisites, []string{"List users before fetching details."}) {
		t.Fatalf("prerequisites = %#v", cmd.Prerequisites)
	}
	if !reflect.DeepEqual(cmd.KnownErrors, []KnownError{{Status: 400, Cause: "missing id"}}) {
		t.Fatalf("known errors = %#v", cmd.KnownErrors)
	}

	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Catalog
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip.Commands[0].Path, cmd.Path) {
		t.Fatalf("round-trip path = %#v", roundTrip.Commands[0].Path)
	}
	if roundTrip.Commands[0].Body.Schema.Properties["name"].Type != "string" {
		t.Fatalf("round-trip body schema = %+v", roundTrip.Commands[0].Body.Schema)
	}
	if !reflect.DeepEqual(roundTrip.Commands[0].KnownErrors, cmd.KnownErrors) {
		t.Fatalf("round-trip known errors = %#v", roundTrip.Commands[0].KnownErrors)
	}
}

func TestBuildCatalog_HiddenCommands(t *testing.T) {
	root := newRootWithModuleGroup()
	Build(root, "demo", []CommandSpec{
		{Group: "Users", Use: "get-user", Short: "Get a user", Method: "GET", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "delete-user", Short: "Delete a user", Method: "DELETE", PathTpl: "/users/{id}", Hidden: true},
	})

	catalog := BuildCatalog(root, CatalogOptions{})
	if len(catalog.Commands) != 1 || catalog.Commands[0].Use != "get-user" {
		t.Fatalf("visible commands = %+v", catalog.Commands)
	}
	catalog = BuildCatalog(root, CatalogOptions{IncludeHidden: true})
	if len(catalog.Commands) != 2 {
		t.Fatalf("all commands = %d, want 2", len(catalog.Commands))
	}
}

func TestFindAndSearchCatalog(t *testing.T) {
	root := newRootWithModuleGroup()
	Build(root, "demo", []CommandSpec{
		{
			Group:       "Users",
			Use:         "get-user",
			Aliases:     []string{"show-user"},
			Short:       "Get a user",
			OperationID: "getUser",
			Method:      "GET",
			PathTpl:     "/users/{id}",
			Params:      []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: "User id"}},
		},
		{
			Group:       "Users",
			Use:         "list-users",
			Short:       "List users",
			OperationID: "listUsers",
			Method:      "GET",
			PathTpl:     "/users",
		},
	})

	cmd, ok := FindCatalogCommand(root, []string{"demo", "users", "get-user"}, CatalogOptions{})
	if !ok || cmd.OperationID != "getUser" {
		t.Fatalf("find = %+v, %v", cmd, ok)
	}
	cmd, ok = FindCatalogCommand(root, []string{"demo", "users", "show-user"}, CatalogOptions{})
	if !ok || !reflect.DeepEqual(cmd.Path, []string{"demo", "users", "get-user"}) {
		t.Fatalf("alias find = %+v, %v", cmd, ok)
	}
	if _, ok := FindCatalogCommand(root, []string{"demo", "users"}, CatalogOptions{}); ok {
		t.Fatal("group container should not resolve as generated command")
	}

	for _, query := range []string{"getUser", "/users/{id}", "show-user", "id"} {
		results := SearchCatalog(root, query, SearchOptions{Limit: 10})
		if len(results) == 0 || results[0].Command.Use != "get-user" {
			t.Fatalf("query %q results = %+v", query, results)
		}
	}

	results := SearchCatalog(root, "users", SearchOptions{Limit: 1})
	if len(results) != 1 {
		t.Fatalf("limited results = %d, want 1", len(results))
	}
}

func TestBuildCatalog_DefaultAuthRequired(t *testing.T) {
	root := newRootWithModuleGroup()
	Build(root, "demo", []CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Short:   "Get a user",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}})

	catalog := BuildCatalog(root, CatalogOptions{})
	if len(catalog.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(catalog.Commands))
	}
	if !catalog.Commands[0].Auth.Required {
		t.Fatal("nil security should require auth to match runtime behavior")
	}
}
