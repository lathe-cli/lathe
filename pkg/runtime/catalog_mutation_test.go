package runtime

import "testing"

func TestCatalogMutation_HTTPMethods(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "TRACE"} {
		if got := catalogMutation(CommandSpec{Method: method, PathTpl: "/users"}); got != MutationRead {
			t.Fatalf("%s = %q", method, got)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if got := catalogMutation(CommandSpec{Method: method, PathTpl: "/users"}); got != MutationWrite {
			t.Fatalf("%s = %q", method, got)
		}
	}
	if got := catalogMutation(CommandSpec{PathTpl: "/users"}); got != MutationUnknown {
		t.Fatalf("empty method = %q", got)
	}
}

func TestCatalogMutation_ExplicitOverride(t *testing.T) {
	if got := catalogMutation(CommandSpec{Method: "POST", PathTpl: "/reports/query", Mutation: MutationRead}); got != MutationRead {
		t.Fatalf("POST override read = %q", got)
	}
	if got := catalogMutation(CommandSpec{Method: "GET", PathTpl: "/trigger", Mutation: MutationWrite}); got != MutationWrite {
		t.Fatalf("GET override write = %q", got)
	}
	overridden := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"mutation CreateApp { createApp { id } }","variables":{}}`,
		},
		Mutation: MutationRead,
	})
	if overridden != MutationRead {
		t.Fatalf("override must beat graphql template = %q", overridden)
	}
}

func TestCatalogMutation_GraphQLTemplate(t *testing.T) {
	mutation := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"mutation CreateApp($name:String!){createApp(name:$name){id}}","variables":{}}`,
		},
	})
	if mutation != MutationWrite {
		t.Fatalf("graphql mutation = %q", mutation)
	}

	query := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"query ListApps { listApps { id } }","variables":{}}`,
		},
	})
	if query != MutationRead {
		t.Fatalf("graphql query = %q", query)
	}

	anon := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"{ listApps { id } }","variables":{}}`,
		},
	})
	if anon != MutationRead {
		t.Fatalf("anonymous graphql query = %q", anon)
	}

	commented := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: "{\"query\":\"# inspect\\nquery GetApp { app { id } }\",\"variables\":{}}",
		},
	})
	if commented != MutationRead {
		t.Fatalf("commented graphql query = %q", commented)
	}
}

func TestCatalogMutation_NonGraphQLTemplateFallsBackToMethod(t *testing.T) {
	got := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/users",
		RequestBody: &RequestBody{
			Template: `{"name":"alice"}`,
		},
	})
	if got != MutationWrite {
		t.Fatalf("rest template = %q", got)
	}
}

func TestCatalogWorkflowMutation_HeaviestStep(t *testing.T) {
	read := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{Method: "GET", PathTpl: "/tenants/{id}"}},
	}})
	if read != MutationRead {
		t.Fatalf("all GET = %q", read)
	}

	mixed := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{Method: "POST", PathTpl: "/tenants"}},
	}})
	if mixed != MutationWrite {
		t.Fatalf("GET+POST = %q", mixed)
	}

	undecidable := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{PathTpl: "/tenants"}},
	}})
	if undecidable != MutationUnknown {
		t.Fatalf("GET+empty method = %q", undecidable)
	}

	write := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{
			Method:  "POST",
			PathTpl: "/graphql",
			RequestBody: &RequestBody{
				Template: `{"query":"mutation CreateApp { createApp { id } }","variables":{}}`,
			},
		}},
	}})
	if write != MutationWrite {
		t.Fatalf("GET+graphql mutation = %q", write)
	}

	if got := catalogWorkflowMutation(WorkflowSpec{}); got != MutationUnknown {
		t.Fatalf("empty workflow = %q", got)
	}
}

func TestCatalogSchemaDocument(t *testing.T) {
	schema := CatalogSchemaDocument()
	if schema.CatalogSchemaVersion != CatalogSchemaVersion {
		t.Fatalf("version = %d", schema.CatalogSchemaVersion)
	}
	if schema.DryRun.Result != DryRunHTTPPreview {
		t.Fatalf("dry-run result = %q", schema.DryRun.Result)
	}
	want := []string{CatalogSurfaceCommands, CatalogSurfaceCommandsShow, CatalogSurfaceCommandsSchema, CatalogSurfaceSearch}
	if len(schema.Surfaces) != len(want) {
		t.Fatalf("surfaces = %#v", schema.Surfaces)
	}
	for i, surface := range want {
		if schema.Surfaces[i] != surface {
			t.Fatalf("surfaces = %#v", schema.Surfaces)
		}
	}
}
