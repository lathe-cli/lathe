package runtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuildCatalog_UsesAttachedSpec(t *testing.T) {
	config.Bind(&config.Manifest{CLI: config.CLIInfo{Name: "myctl"}, Contexts: map[string]config.ContextInfo{
		"workspace": {Env: "MYCTL_WORKSPACE_ID"},
	}})
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
				{Name: "user_id", Flag: "user-id", Aliases: []string{"user_id"}, Argument: "id", In: InPath, GoType: "string", Required: true, Help: "User id"},
				{Name: "workspace", Flag: "workspace", In: InQuery, GoType: "string", Default: "default", Enum: []string{"default", "prod"}, Format: "slug", Help: "Target workspace"},
			},
			RequestBody: &RequestBody{
				Required:  true,
				MediaType: "application/json",
				Schema:    &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{"name": {Type: "string"}}},
				RuntimeSchema: &RuntimeSchemaSpec{
					Operation: CommandSpec{OperationID: "describeUser", Method: "GET", PathTpl: "/users/{id}", DefaultHostname: "api.example.com", Params: []ParamSpec{
						{Name: "workspace_id", Flag: "workspace-id", In: InQuery, GoType: "string", Context: "workspace"},
					}},
					ResponsePath: "input_schema",
					Params:       map[string]string{"id": "${params.user_id}"},
				},
				SetOnlyFields: []string{"limits"},
			},
			Output: OutputHints{
				ListPath:       "data.items",
				DefaultColumns: []string{"id", "name"},
				ColumnLabels:   map[string]string{"id": "ID"},
				ColumnFormats: map[string]ColumnFormat{
					"id": {Kind: "currency", Currency: "USD", SourceScale: 6, Grouping: true, MinFractionDigits: 2, MaxFractionDigits: 6},
				},
				ColumnAlignments:  map[string]string{"name": "left"},
				ResponseMediaType: "application/json",
				Pagination:        &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token", LimitParam: "limit"},
				Streaming: &StreamingHint{Strategy: "sse", Policy: &StreamPolicy{
					DataFormat: "json", EventNamePath: "kind",
					Collect: &StreamCollectHint{RequireStop: true, StopEvents: []string{"done"}},
					Live:    &StreamLiveHint{Events: []string{"chunk"}, From: "text"},
				}},
			},
			Security:      &SecurityHint{Scopes: []string{"users:read"}},
			Notes:         []string{"Use the canonical user ID."},
			Prerequisites: []string{"List users before fetching details."},
			KnownErrors:   []KnownError{{Status: 400, Cause: "missing id"}},
			SearchTerms:   []string{"account", "profile"},
		},
	})

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl", CLIVersion: "v1.2.3"})
	testutil.Require(t, catalog.CatalogSchemaVersion == CatalogSchemaVersion, "schema = %d, want %d", catalog.CatalogSchemaVersion, CatalogSchemaVersion)
	testutil.Require(t, catalog.CLI.Name == "myctl" && catalog.CLI.Version == "v1.2.3", "cli = %+v", catalog.CLI)
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d, want 1", len(catalog.Commands))

	cmd := catalog.Commands[0]
	testutil.Require(t, cmd.Kind == "operation", "kind = %q", cmd.Kind)
	testutil.Require(t, reflect.DeepEqual(cmd.Path, []string{"demo", "users", "get-user"}), "path = %#v", cmd.Path)
	testutil.Require(t, cmd.Group == "Users", "group = %q, want original casing", cmd.Group)
	testutil.Require(t, cmd.Service == "demo" && cmd.Use == "get-user" && cmd.OperationID == "getUser", "command identity = %+v", cmd)
	testutil.Require(t, cmd.Auth.Required == true && reflect.DeepEqual(cmd.Auth.Scopes, []string{"users:read"}), "auth = %+v", cmd.Auth)
	testutil.Require(t, cmd.Mutation == MutationRead, "mutation = %q", cmd.Mutation)
	testutil.Require(t, cmd.DryRun != nil && cmd.DryRun.Mode == DryRunHTTPPreview && cmd.DryRun.Flag == "dry-run", "dry_run = %+v", cmd.DryRun)
	testutil.Require(t, cmd.HTTP.DefaultHostname == "api.example.com", "http = %+v", cmd.HTTP)
	testutil.Require(t, len(cmd.Examples) == 1 && cmd.Examples[0].Summary == "Get a user by ID" && cmd.Examples[0].OutputHints.IDPath == "data.user.id", "examples = %+v", cmd.Examples)
	testutil.Require(t, string(cmd.Examples[0].BodyShape) == `{"input":{"name":"..."}}`, "body shape = %s", cmd.Examples[0].BodyShape)
	testutil.Require(t, reflect.DeepEqual(cmd.Examples[0].FollowUpCommands, []string{"myctl demo users list-users -o json"}), "follow-up commands = %#v", cmd.Examples[0].FollowUpCommands)
	testutil.Require(t, cmd.Body != nil && cmd.Body.Required && cmd.Body.MediaType == "application/json", "body = %+v", cmd.Body)
	testutil.Require(t, cmd.Body.Schema != nil && cmd.Body.Schema.Properties["name"].Type == "string", "body schema = %+v", cmd.Body.Schema)
	testutil.Require(t, cmd.Body.RuntimeSchema != nil && cmd.Body.RuntimeSchema.OperationID == "describeUser" && cmd.Body.RuntimeSchema.HTTP.PathTemplate == "/users/{id}" && cmd.Body.RuntimeSchema.ResponsePath == "input_schema" && cmd.Body.RuntimeSchema.Params["id"] == "${params.user_id}", "runtime schema = %+v", cmd.Body.RuntimeSchema)
	contexts := cmd.Body.RuntimeSchema.Contexts
	testutil.Require(t, len(contexts) == 1 && contexts[0].Name == "workspace" && contexts[0].Env == "MYCTL_WORKSPACE_ID" && reflect.DeepEqual(contexts[0].Precedence, []string{"flag", "env", "stored"}), "runtime schema contexts = %#v", contexts)
	testutil.Require(t, len(cmd.Flags) == 2, "flags = %d, want 2", len(cmd.Flags))
	testutil.Require(t, cmd.Flags[0].Location == InPath && cmd.Flags[0].Required, "path flag = %+v", cmd.Flags[0])
	testutil.Require(t, cmd.Flags[0].Name == "user_id" && cmd.Flags[0].Flag == "user-id" && reflect.DeepEqual(cmd.Flags[0].Aliases, []string{"user_id"}) && cmd.Flags[0].Argument == "id" && cmd.Flags[0].Position == 1, "path input syntax = %+v", cmd.Flags[0])
	testutil.Require(t, cmd.Flags[1].Default == "default" && reflect.DeepEqual(cmd.Flags[1].Enum, []string{"default", "prod"}), "query flag = %+v", cmd.Flags[1])
	testutil.Require(t, cmd.Output.Pagination != nil && cmd.Output.Pagination.TokenParam == "page_token", "pagination = %+v", cmd.Output.Pagination)
	testutil.Require(t, cmd.Output.ColumnLabels["id"] == "ID" && cmd.Output.ColumnFormats["id"].SourceScale == 6 && cmd.Output.ColumnAlignments["name"] == "left", "column metadata = %+v", cmd.Output)
	testutil.Require(t, cmd.Output.Streaming != nil && cmd.Output.Streaming.Strategy == "sse", "streaming = %+v", cmd.Output.Streaming)
	testutil.Require(t, cmd.Output.Streaming.Policy != nil && cmd.Output.Streaming.Policy.Collect != nil && cmd.Output.Streaming.Policy.Collect.RequireStop, "streaming policy = %+v", cmd.Output.Streaming.Policy)
	testutil.Require(t, reflect.DeepEqual(cmd.Notes, []string{"Use the canonical user ID."}), "notes = %#v", cmd.Notes)
	testutil.Require(t, reflect.DeepEqual(cmd.Prerequisites, []string{"List users before fetching details."}), "prerequisites = %#v", cmd.Prerequisites)
	testutil.Require(t, reflect.DeepEqual(cmd.KnownErrors, []KnownError{{Status: 400, Cause: "missing id"}}), "known errors = %#v", cmd.KnownErrors)
	testutil.Require(t, reflect.DeepEqual(cmd.SearchTerms, []string{"account", "profile"}), "search terms = %#v", cmd.SearchTerms)
	testutil.Require(t, reflect.DeepEqual(cmd.Body.SetOnlyFields, []string{"limits"}), "set-only fields = %#v", cmd.Body.SetOnlyFields)

	raw, err := json.Marshal(catalog)
	testutil.Require(t, err == nil, "%v", err)
	var roundTrip Catalog
	testutil.NoError(t, json.Unmarshal(raw, &roundTrip))
	testutil.Require(t, reflect.DeepEqual(roundTrip.Commands[0].Path, cmd.Path), "round-trip path = %#v", roundTrip.Commands[0].Path)
	testutil.Require(t, roundTrip.Commands[0].Body.Schema.Properties["name"].Type == "string", "round-trip body schema = %+v", roundTrip.Commands[0].Body.Schema)
	testutil.Require(t, roundTrip.Commands[0].Body.RuntimeSchema != nil && roundTrip.Commands[0].Body.RuntimeSchema.OperationID == "describeUser" && reflect.DeepEqual(roundTrip.Commands[0].Body.RuntimeSchema.Contexts, contexts), "round-trip runtime schema = %+v", roundTrip.Commands[0].Body.RuntimeSchema)
	testutil.Require(t, reflect.DeepEqual(roundTrip.Commands[0].KnownErrors, cmd.KnownErrors), "round-trip known errors = %#v", roundTrip.Commands[0].KnownErrors)
	testutil.Require(t, reflect.DeepEqual(roundTrip.Commands[0].Output.ColumnLabels, cmd.Output.ColumnLabels) && reflect.DeepEqual(roundTrip.Commands[0].Output.ColumnFormats, cmd.Output.ColumnFormats) && reflect.DeepEqual(roundTrip.Commands[0].Output.ColumnAlignments, cmd.Output.ColumnAlignments), "round-trip column metadata = %#v", roundTrip.Commands[0].Output)
	testutil.Require(t, len(roundTrip.Commands[0].Examples) == 1 && roundTrip.Commands[0].Examples[0].OutputHints.IDPath == "data.user.id", "round-trip examples = %#v", roundTrip.Commands[0].Examples)
}

func TestBuildCatalog_ExposesContextContract(t *testing.T) {
	config.Bind(&config.Manifest{CLI: config.CLIInfo{Name: "myctl"}, Contexts: map[string]config.ContextInfo{
		"workspace": {Env: "MYCTL_WORKSPACE_ID"},
	}})
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
		Group: "Apps", Use: "use", Method: "POST", PathTpl: "/workspaces/{workspace_id}",
		Params:     []ParamSpec{{Name: "workspace_id", Flag: "workspace-id", In: InPath, GoType: "string", Required: true, Context: "workspace"}},
		SetContext: &ContextSetHint{Name: "workspace", Param: "workspace_id"},
	}})
	command := BuildCatalog(root, CatalogOptions{CLIName: "myctl"}).Commands[0]
	context := command.Flags[0].Context
	testutil.Require(t, context != nil && context.Name == "workspace" && context.Env == "MYCTL_WORKSPACE_ID" && reflect.DeepEqual(context.Precedence, []string{"flag", "env", "stored"}), "flag context = %#v", context)
	testutil.Require(t, command.SetsContext != nil && command.SetsContext.Name == "workspace" && command.SetsContext.FromParam == "workspace_id", "sets context = %#v", command.SetsContext)
}

func TestBuildCatalog_WorkflowCommand(t *testing.T) {
	root := newRootWithModuleGroup()
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
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
	}}))

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl"})
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d", len(catalog.Commands))
	cmd := catalog.Commands[0]
	testutil.Require(t, cmd.Kind == "workflow", "kind = %q", cmd.Kind)
	testutil.Require(t, reflect.DeepEqual(cmd.Path, []string{"doctor"}), "path = %#v", cmd.Path)
	testutil.Require(t, cmd.Workflow != nil && cmd.Workflow.DSL == "lathe.workflow.v1" && cmd.Workflow.OutputFrom == "${steps.tenant}", "workflow = %+v", cmd.Workflow)
	testutil.Require(t, len(cmd.Workflow.Steps) == 2 && cmd.Workflow.Steps[1].OperationID == "checkTenant", "steps = %+v", cmd.Workflow.Steps)
	testutil.Require(t, len(cmd.Flags) == 1 && cmd.Flags[0].Location == InInput, "flags = %+v", cmd.Flags)
	testutil.Require(t, cmd.Auth.Required, "auth = %+v", cmd.Auth)
	testutil.Require(t, cmd.Mutation == MutationRead, "workflow mutation = %q", cmd.Mutation)
	testutil.Require(t, cmd.DryRun != nil && cmd.DryRun.Mode == DryRunUnsupported && cmd.DryRun.Flag == "", "workflow dry_run = %+v", cmd.DryRun)
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
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d, want 1", len(catalog.Commands))
	cmd := catalog.Commands[0]
	testutil.Require(t, cmd.Example == "myctl demo users get-user --id 123 -o json", "legacy example = %q", cmd.Example)
	testutil.Require(t, len(cmd.Examples) == 1 && cmd.Examples[0].Command == cmd.Example, "projected examples = %#v", cmd.Examples)
}

func TestBuildCatalog_BodyFlagLocation(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Keys",
		Use:     "replace-limits",
		Method:  "PATCH",
		PathTpl: "/keys/{id}/limits",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true},
			{Name: "allowedModels", Flag: "allowed-models", In: InBody, GoType: "[]string", ItemEnum: []string{"model-a", "model-b"}},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
	}})
	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl"})
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d", len(catalog.Commands))
	var bodyFlag *CatalogFlag
	for i := range catalog.Commands[0].Flags {
		if catalog.Commands[0].Flags[i].Name == "allowedModels" {
			bodyFlag = &catalog.Commands[0].Flags[i]
		}
	}
	testutil.Require(t, bodyFlag != nil && bodyFlag.Location == InBody && bodyFlag.Flag == "allowed-models" && reflect.DeepEqual(bodyFlag.ItemEnum, []string{"model-a", "model-b"}), "body flag = %#v", bodyFlag)
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
				Type: "object",
				Properties: map[string]*SchemaSpec{
					"name":   {Type: "string", Nullable: true},
					"labels": {Type: "object", AdditionalProperties: &AdditionalPropertiesSpec{Schema: &SchemaSpec{Type: "string"}}},
				},
				Required: []string{"name"},
			},
			Template:  tmpl,
			MergePath: "variables",
		},
	}})

	catalog := BuildCatalog(root, CatalogOptions{CLIName: "myctl"})
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d, want 1", len(catalog.Commands))
	body := catalog.Commands[0].Body
	testutil.Require(t, body != nil && body.Template == tmpl && body.MergePath == "variables", "catalog body envelope = %+v", body)
	testutil.Require(t, catalog.Commands[0].Mutation == MutationWrite, "graphql mutation = %q", catalog.Commands[0].Mutation)

	raw, err := json.Marshal(catalog)
	testutil.Require(t, err == nil, "%v", err)
	for _, want := range []string{`"template":`, `"merge_path":"variables"`, `"required":["name"]`, `"nullable":true`, `"additionalProperties":{"type":"string"}`, `createApp(name:$name)`} {
		testutil.Require(t, strings.Contains(string(raw), want), "catalog JSON missing %q:\n%s", want, raw)
	}
	var roundTrip Catalog
	testutil.NoError(t, json.Unmarshal(raw, &roundTrip))
	rt := roundTrip.Commands[0].Body
	testutil.Require(t, rt != nil && rt.Template == tmpl && rt.MergePath == "variables", "round-trip body envelope = %+v", rt)
	testutil.Require(t, rt.Schema != nil && reflect.DeepEqual(rt.Schema.Required, []string{"name"}) && rt.Schema.Properties["name"] != nil && rt.Schema.Properties["name"].Nullable, "round-trip body schema = %+v", rt.Schema)
	labels := rt.Schema.Properties["labels"]
	testutil.Require(t, labels != nil && labels.AdditionalProperties != nil && labels.AdditionalProperties.Schema != nil && labels.AdditionalProperties.Schema.Type == "string", "round-trip labels schema = %+v", labels)
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
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d, want 1", len(catalog.Commands))
	flags := catalog.Commands[0].Flags
	testutil.Require(t, reflect.DeepEqual(flags[0].InputModes, []string{"flag", "env", "file", "stdin"}), "api-key input modes = %#v", flags[0].InputModes)
	testutil.Require(t, flags[1].InputModes == nil, "name input modes = %#v", flags[1].InputModes)
}

func TestBuildCatalog_WorkflowStepConditions(t *testing.T) {
	root := newRootWithModuleGroup()
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "deploy",
		Steps: []WorkflowStepSpec{{
			ID: "gpu",
			Operation: CommandSpec{
				Group:       "Apps",
				Use:         "deploy-gpu",
				Method:      "POST",
				PathTpl:     "/apps/gpu",
				OperationID: "Apps_DeployGPU",
			},
			When: []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
		}},
	}}))

	catalog := BuildCatalog(root, CatalogOptions{})
	testutil.Require(t, catalog.CatalogSchemaVersion == CatalogSchemaVersion, "schema version = %d, want %d", catalog.CatalogSchemaVersion, CatalogSchemaVersion)
	var step *CatalogWorkflowStep
	for i, entry := range catalog.Commands {
		if entry.Kind == "workflow" && entry.Workflow != nil && len(entry.Workflow.Steps) > 0 {
			step = &catalog.Commands[i].Workflow.Steps[0]
			break
		}
	}
	if step == nil {
		t.Fatal("no workflow step in catalog")
		return
	}
	want := []CatalogWorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}}
	testutil.Require(t, reflect.DeepEqual(step.When, want), "when = %#v, want %#v", step.When, want)

	data, err := json.Marshal(step)
	testutil.Require(t, err == nil, "marshal: %v", err)
	testutil.Require(t, strings.Contains(string(data), `"when":[{"value":"${input.kind}","operator":"in","values":["gpu"]}]`), "catalog JSON = %s", data)
}

func TestBuildCatalog_UnconditionalStepOmitsWhen(t *testing.T) {
	data, err := json.Marshal(CatalogWorkflowStep{ID: "plain"})
	testutil.Require(t, err == nil, "marshal: %v", err)
	testutil.Require(t, !strings.Contains(string(data), "when"), "catalog JSON = %s", data)
}
