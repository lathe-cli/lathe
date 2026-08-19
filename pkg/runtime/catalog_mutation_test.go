package runtime

import "testing"

func TestCatalogMutation_HTTPMethods(t *testing.T) {
	if got := catalogMutation(CommandSpec{Method: "GET", PathTpl: "/users"}); got != MutationRead {
		t.Fatalf("GET = %q", got)
	}
	if got := catalogMutation(CommandSpec{Method: "HEAD", PathTpl: "/users"}); got != MutationRead {
		t.Fatalf("HEAD = %q", got)
	}
	if got := catalogMutation(CommandSpec{Method: "POST", PathTpl: "/users"}); got != MutationUnknown {
		t.Fatalf("POST without GraphQL = %q", got)
	}
	if got := catalogMutation(CommandSpec{Method: "DELETE", PathTpl: "/users/{id}"}); got != MutationUnknown {
		t.Fatalf("DELETE = %q", got)
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

func TestCatalogMutation_NonGraphQLTemplateStaysUnknown(t *testing.T) {
	got := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/users",
		RequestBody: &RequestBody{
			Template: `{"name":"alice"}`,
		},
	})
	if got != MutationUnknown {
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

	unknown := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{Method: "POST", PathTpl: "/tenants"}},
	}})
	if unknown != MutationUnknown {
		t.Fatalf("GET+POST = %q", unknown)
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
