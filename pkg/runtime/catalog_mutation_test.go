package runtime

import (
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

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
	testutil.Require(t, overridden == MutationRead, "override must beat graphql template = %q", overridden)
}

func TestCatalogMutation_GraphQLTemplate(t *testing.T) {
	mutation := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"mutation CreateApp($name:String!){createApp(name:$name){id}}","variables":{}}`,
		},
	})
	testutil.Require(t, mutation == MutationWrite, "graphql mutation = %q", mutation)

	query := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"query ListApps { listApps { id } }","variables":{}}`,
		},
	})
	testutil.Require(t, query == MutationRead, "graphql query = %q", query)

	anon := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: `{"query":"{ listApps { id } }","variables":{}}`,
		},
	})
	testutil.Require(t, anon == MutationRead, "anonymous graphql query = %q", anon)

	commented := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/graphql",
		RequestBody: &RequestBody{
			Template: "{\"query\":\"# inspect\\nquery GetApp { app { id } }\",\"variables\":{}}",
		},
	})
	testutil.Require(t, commented == MutationRead, "commented graphql query = %q", commented)
}

func TestCatalogMutation_NonGraphQLTemplateFallsBackToMethod(t *testing.T) {
	got := catalogMutation(CommandSpec{
		Method:  "POST",
		PathTpl: "/users",
		RequestBody: &RequestBody{
			Template: `{"name":"alice"}`,
		},
	})
	testutil.Require(t, got == MutationWrite, "rest template = %q", got)
}

func TestCatalogWorkflowMutation_HeaviestStep(t *testing.T) {
	read := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{Method: "GET", PathTpl: "/tenants/{id}"}},
	}})
	testutil.Require(t, read == MutationRead, "all GET = %q", read)

	mixed := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{Method: "POST", PathTpl: "/tenants"}},
	}})
	testutil.Require(t, mixed == MutationWrite, "GET+POST = %q", mixed)

	undecidable := catalogWorkflowMutation(WorkflowSpec{Steps: []WorkflowStepSpec{
		{Operation: CommandSpec{Method: "GET", PathTpl: "/health"}},
		{Operation: CommandSpec{PathTpl: "/tenants"}},
	}})
	testutil.Require(t, undecidable == MutationUnknown, "GET+empty method = %q", undecidable)

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
	testutil.Require(t, write == MutationWrite, "GET+graphql mutation = %q", write)

	if got := catalogWorkflowMutation(WorkflowSpec{}); got != MutationUnknown {
		t.Fatalf("empty workflow = %q", got)
	}
}

func TestCatalogSchemaDocument(t *testing.T) {
	schema := CatalogSchemaDocument()
	testutil.Require(t, schema.CatalogSchemaVersion == CatalogSchemaVersion, "version = %d", schema.CatalogSchemaVersion)
	testutil.Require(t, schema.DryRun.Result == DryRunHTTPPreview, "dry-run result = %q", schema.DryRun.Result)
	want := []string{CatalogSurfaceCommands, CatalogSurfaceCommandsShow, CatalogSurfaceCommandsSchema, CatalogSurfaceSearch}
	testutil.Require(t, len(schema.Surfaces) == len(want), "surfaces = %#v", schema.Surfaces)
	for i, surface := range want {
		testutil.Require(t, schema.Surfaces[i] == surface, "surfaces = %#v", schema.Surfaces)
	}
}
