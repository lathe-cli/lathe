package runtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBuildCatalog_UsesAttachedSpec(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{
			Group:   "Users",
			Use:     "get-user",
			Aliases: []string{"show-user"},
			Short:   "Get a user",
			Long:    "Get one user by id.",
			Example: "myctl demo users get-user --id 123 -o json",
			Examples: []CommandExample{{
				Summary:          "Get a user by ID",
				Command:          "myctl demo users get-user --id 123 -o json",
				BodyShape:        []byte(`{"input":{"name":"..."}}`),
				OutputHints:      &ExampleOutputHints{IDPath: "data.user.id", ListPath: "data.items"},
				FollowUpCommands: []string{"myctl demo users list-users -o json"},
			}},
			OperationID:     "getUser",
			Method:          "GET",
			PathTpl:         "/users/{id}",
			DefaultHostname: "api.example.com",
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
	if cmd.Kind != "operation" {
		t.Fatalf("kind = %q", cmd.Kind)
	}
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
	if cmd.HTTP.DefaultHostname != "api.example.com" {
		t.Fatalf("http = %+v", cmd.HTTP)
	}
	if len(cmd.Examples) != 1 || cmd.Examples[0].Summary != "Get a user by ID" || cmd.Examples[0].OutputHints.IDPath != "data.user.id" {
		t.Fatalf("examples = %+v", cmd.Examples)
	}
	if string(cmd.Examples[0].BodyShape) != `{"input":{"name":"..."}}` {
		t.Fatalf("body shape = %s", cmd.Examples[0].BodyShape)
	}
	if !reflect.DeepEqual(cmd.Examples[0].FollowUpCommands, []string{"myctl demo users list-users -o json"}) {
		t.Fatalf("follow-up commands = %#v", cmd.Examples[0].FollowUpCommands)
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
	if len(roundTrip.Commands[0].Examples) != 1 || roundTrip.Commands[0].Examples[0].OutputHints.IDPath != "data.user.id" {
		t.Fatalf("round-trip examples = %#v", roundTrip.Commands[0].Examples)
	}
}

func TestBuildCatalog_WorkflowCommand(t *testing.T) {
	root := newRootWithModuleGroup()
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use:   "doctor",
		Short: "Check API health",
		Params: []ParamSpec{
			{Name: "tenant", Flag: "tenant", In: InInput, GoType: "string", Required: true},
		},
		Steps: []WorkflowStepSpec{
			{
				ID: "health",
				Operation: CommandSpec{
					OperationID: "getHealth",
					Method:      "GET",
					PathTpl:     "/health",
					Security:    &SecurityHint{Public: true},
				},
			},
			{
				ID: "tenant",
				Operation: CommandSpec{
					OperationID: "checkTenant",
					Method:      "GET",
					PathTpl:     "/tenants/{tenant}",
					Params: []ParamSpec{
						{Name: "tenant", Flag: "tenant", In: InPath, GoType: "string", Required: true},
					},
				},
			},
		},
		OutputFrom: "${steps.tenant}",
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl"})
	if len(catalog.Commands) != 1 {
		t.Fatalf("commands = %d", len(catalog.Commands))
	}
	cmd := catalog.Commands[0]
	if cmd.Kind != "workflow" {
		t.Fatalf("kind = %q", cmd.Kind)
	}
	if !reflect.DeepEqual(cmd.Path, []string{"doctor"}) {
		t.Fatalf("path = %#v", cmd.Path)
	}
	if cmd.Workflow == nil || cmd.Workflow.DSL != "lathe.workflow.v1" || cmd.Workflow.OutputFrom != "${steps.tenant}" {
		t.Fatalf("workflow = %+v", cmd.Workflow)
	}
	if len(cmd.Workflow.Steps) != 2 || cmd.Workflow.Steps[1].OperationID != "checkTenant" {
		t.Fatalf("steps = %+v", cmd.Workflow.Steps)
	}
	if len(cmd.Flags) != 1 || cmd.Flags[0].Location != InInput {
		t.Fatalf("flags = %+v", cmd.Flags)
	}
	if !cmd.Auth.Required {
		t.Fatalf("auth = %+v", cmd.Auth)
	}
}

func TestBuildCatalog_ProjectsLegacyExample(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Short:   "Get a user",
		Example: "myctl demo users get-user --id 123 -o json",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}})

	catalog := BuildCatalog(root, CatalogOptions{})
	if len(catalog.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(catalog.Commands))
	}
	cmd := catalog.Commands[0]
	if cmd.Example != "myctl demo users get-user --id 123 -o json" {
		t.Fatalf("legacy example = %q", cmd.Example)
	}
	if len(cmd.Examples) != 1 || cmd.Examples[0].Command != cmd.Example {
		t.Fatalf("projected examples = %#v", cmd.Examples)
	}
}

func TestBuildCatalog_RequestBodyEnvelope(t *testing.T) {
	root := newRootWithModuleGroup()
	const tmpl = `{"query":"mutation CreateApp($name:String!){createApp(name:$name){id}}","variables":{}}`
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:       "Apps",
		Use:         "create-app",
		Short:       "Create an app",
		OperationID: "Apps_CreateApp",
		Method:      "POST",
		PathTpl:     "/graphql",
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &SchemaSpec{
				Type:       "object",
				Properties: map[string]*SchemaSpec{"name": {Type: "string"}},
				Required:   []string{"name"},
			},
			Template:  tmpl,
			MergePath: "variables",
		},
	}})

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl"})
	if len(catalog.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(catalog.Commands))
	}
	body := catalog.Commands[0].Body
	if body == nil || body.Template != tmpl || body.MergePath != "variables" {
		t.Fatalf("catalog body envelope = %+v", body)
	}

	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"template":`, `"merge_path":"variables"`, `"required":["name"]`, `createApp(name:$name)`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("catalog JSON missing %q:\n%s", want, raw)
		}
	}
	var roundTrip Catalog
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	rt := roundTrip.Commands[0].Body
	if rt == nil || rt.Template != tmpl || rt.MergePath != "variables" {
		t.Fatalf("round-trip body envelope = %+v", rt)
	}
	if rt.Schema == nil || !reflect.DeepEqual(rt.Schema.Required, []string{"name"}) {
		t.Fatalf("round-trip body schema = %+v", rt.Schema)
	}
}

func TestBuildCatalog_SensitiveFlagInputModes(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:       "Credentials",
		Use:         "create-credential",
		Short:       "Create credential",
		OperationID: "Credentials_Create",
		Method:      "POST",
		PathTpl:     "/credentials",
		Params: []ParamSpec{
			{Name: "apiKey", Flag: "api-key", In: InQuery, GoType: "string", Required: true, Help: "API key"},
			{Name: "name", Flag: "name", In: InQuery, GoType: "string", Required: true, Help: "Name"},
		},
	}})

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl"})
	if len(catalog.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(catalog.Commands))
	}
	flags := catalog.Commands[0].Flags
	if !reflect.DeepEqual(flags[0].InputModes, []string{"flag", "env", "file", "stdin"}) {
		t.Fatalf("api-key input modes = %#v", flags[0].InputModes)
	}
	if flags[1].InputModes != nil {
		t.Fatalf("name input modes = %#v", flags[1].InputModes)
	}
}

func TestBuildCatalog_HiddenCommands(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
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

func TestBuildCatalog_Capabilities(t *testing.T) {
	root := newRootWithModuleGroup()
	AttachCapability(root, CapabilitySkillBundle)
	AttachCapability(root, CapabilitySkillBundle)

	catalog := BuildCatalog(root, CatalogOptions{Capabilities: []string{"trace"}})
	if !reflect.DeepEqual(catalog.CLI.Capabilities, []string{"skill.bundle", "trace"}) {
		t.Fatalf("capabilities = %#v", catalog.CLI.Capabilities)
	}
}

func TestFindAndSearchCatalog(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
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

func TestSearchCatalog_SoftMatchesNoisyIntent(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{
			Group:       "Users",
			Use:         "get-user",
			Aliases:     []string{"show-user"},
			Short:       "Get a user",
			OperationID: "getUser",
			Method:      "GET",
			PathTpl:     "/users/{id}",
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

	results := SearchCatalog(root, "get user stray", SearchOptions{Limit: 10})
	if len(results) == 0 || results[0].Command.Use != "get-user" {
		t.Fatalf("noisy get user results = %+v", results)
	}

	results = SearchCatalog(root, "show_user stray", SearchOptions{Limit: 10})
	if len(results) == 0 || results[0].Command.Use != "get-user" {
		t.Fatalf("normalized alias results = %+v", results)
	}

	results = SearchCatalog(root, "doesnotexist", SearchOptions{Limit: 10})
	if len(results) != 0 {
		t.Fatalf("unknown query results = %+v", results)
	}
}

func TestBuildCatalog_DefaultAuthRequired(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
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
