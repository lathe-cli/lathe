package graphql

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const consoleSDL = `
type Query {
  apps(first: Int): [App!]!
  app(id: ID!): App
  secret: String
}

type Mutation {
  createApp(name: String!): App!
  deleteApp(id: ID!): Boolean!
}

type App {
  id: ID!
  name: String!
  owner: User
}

type User {
  id: ID!
  email: String!
}
`

func parseSDL(t *testing.T, sdl string, queries, mutations []string) (*rawir.RawModule, error) {
	t.Helper()
	return parseSDLWithConfig(t, sdl, &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{Queries: queries, Mutations: mutations},
	})
}

func parseSDLWithConfig(t *testing.T, sdl string, cfg *sourceconfig.GraphQLConfig) (*rawir.RawModule, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(sdl), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &sourceconfig.Source{
		Name:    "console",
		Backend: sourceconfig.BackendGraphQL,
		GraphQL: cfg,
	}
	return Parse(src, dir)
}

func byID(ops []rawir.RawOperation) map[string]rawir.RawOperation {
	m := make(map[string]rawir.RawOperation, len(ops))
	for _, op := range ops {
		m[op.OperationID] = op
	}
	return m
}

func queryDocument(t *testing.T, op rawir.RawOperation) string {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(op.RequestBody.Template), &env); err != nil {
		t.Fatalf("template is not valid JSON: %v", err)
	}
	q, _ := env["query"].(string)
	return q
}

func TestParse_ExposesOnlyPolicyMatchedOps(t *testing.T) {
	mod, err := parseSDL(t, consoleSDL, []string{"app*"}, []string{"createApp"})
	if err != nil {
		t.Fatal(err)
	}
	ops := byID(mod.Operations)
	if len(mod.Operations) != 3 {
		t.Fatalf("ops = %d, want 3 (got %v)", len(mod.Operations), ops)
	}
	for _, want := range []string{"console_apps", "console_app", "console_createApp"} {
		if _, ok := ops[want]; !ok {
			t.Errorf("missing exposed op %q", want)
		}
	}
	for _, no := range []string{"console_secret", "console_deleteApp"} {
		if _, ok := ops[no]; ok {
			t.Errorf("op %q must not be exposed", no)
		}
	}
}

func TestParse_BakesEnvelopeAndVariables(t *testing.T) {
	mod, err := parseSDL(t, consoleSDL, []string{"apps"}, []string{"createApp"})
	if err != nil {
		t.Fatal(err)
	}
	ops := byID(mod.Operations)

	create := ops["console_createApp"]
	if create.Method != "POST" || create.Path != "/graphql" {
		t.Fatalf("http = %s %s, want POST /graphql", create.Method, create.Path)
	}
	if create.RequestBody == nil || create.RequestBody.MergePath != "variables" {
		t.Fatalf("envelope = %+v", create.RequestBody)
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(create.RequestBody.Template), &env); err != nil {
		t.Fatalf("template is not valid JSON: %v", err)
	}
	q, _ := env["query"].(string)
	for _, want := range []string{
		"mutation createApp($name: String!)",
		"createApp(name: $name)",
		"id", "name", "owner", "email",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("baked query missing %q:\n%s", want, q)
		}
	}
	if _, ok := env["variables"].(map[string]any); !ok {
		t.Errorf("variables envelope missing/!object: %#v", env["variables"])
	}

	if len(create.Parameters) != 1 {
		t.Fatalf("createApp params = %+v, want 1", create.Parameters)
	}
	p := create.Parameters[0]
	if p.Name != "name" || p.In != "variable" || !p.Required || p.Type != "string" {
		t.Fatalf("name variable = %+v", p)
	}

	apps := ops["console_apps"]
	if len(apps.Parameters) != 1 {
		t.Fatalf("apps params = %+v, want 1", apps.Parameters)
	}
	if ap := apps.Parameters[0]; ap.Name != "first" || ap.Required || ap.Type != "integer" {
		t.Fatalf("first variable = %+v", ap)
	}
}

func TestParse_GeneratesValidGraphQLDocuments(t *testing.T) {
	mod, err := parseSDL(t, consoleSDL, []string{"*"}, []string{"createApp"})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: consoleSDL})
	if err != nil {
		t.Fatalf("reload schema: %v", err)
	}
	for _, op := range mod.Operations {
		var env map[string]any
		if err := json.Unmarshal([]byte(op.RequestBody.Template), &env); err != nil {
			t.Fatalf("%s: bad template: %v", op.OperationID, err)
		}
		q, _ := env["query"].(string)
		if _, errs := gqlparser.LoadQueryWithRules(schema, q, rules.NewDefaultRules()); len(errs) != 0 {
			t.Errorf("%s: generated invalid document: %v\n%s", op.OperationID, errs, q)
		}
	}
}

func TestParse_NormalizesToDistinctCommands(t *testing.T) {
	mod, err := parseSDL(t, consoleSDL, []string{"apps"}, []string{"createApp"})
	if err != nil {
		t.Fatal(err)
	}
	specs := normalize.Normalize(mod)
	if len(specs) != 2 {
		t.Fatalf("specs = %d, want 2", len(specs))
	}
	byUse := make(map[string]runtime.CommandSpec, len(specs))
	for _, s := range specs {
		byUse[s.Use] = s
	}
	for _, use := range []string{"apps", "create-app"} {
		s, ok := byUse[use]
		if !ok {
			t.Fatalf("missing command %q (got %v)", use, byUse)
		}
		if s.Method != "POST" || s.PathTpl != "/graphql" {
			t.Errorf("%s: http = %s %s, want POST /graphql", use, s.Method, s.PathTpl)
		}
		if s.RequestBody == nil || s.RequestBody.MergePath != "variables" || s.RequestBody.Template == "" {
			t.Errorf("%s: envelope = %+v", use, s.RequestBody)
		}
	}
	create := byUse["create-app"]
	if len(create.Params) != 1 {
		t.Fatalf("create-app params = %+v, want 1", create.Params)
	}
	if p := create.Params[0]; p.In != runtime.InVariable || p.Flag != "name" || !p.Required || p.GoType != "string" {
		t.Fatalf("create-app variable flag = %+v", p)
	}
}

func TestParse_AppliesGraphQLGroupAndOutputPolicy(t *testing.T) {
	cfg := &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{
			Queries: []string{"apps"},
		},
		Groups: []sourceconfig.GraphQLGroupPolicy{
			{Match: []string{"app*"}, Group: "Applications"},
		},
		Output: []sourceconfig.GraphQLOutputPolicy{
			{Match: []string{"apps"}, ListPath: "data.apps.nodes", DefaultColumns: []string{"id", "name"}},
		},
	}
	mod, err := parseSDLWithConfig(t, consoleSDL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	apps := byID(mod.Operations)["console_apps"]
	if apps.Group != "Applications" {
		t.Fatalf("raw group = %q, want Applications", apps.Group)
	}
	if apps.Output == nil || apps.Output.ListPath != "data.apps.nodes" || strings.Join(apps.Output.DefaultColumns, ",") != "id,name" {
		t.Fatalf("raw output = %+v", apps.Output)
	}
	specs := normalize.Normalize(mod)
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	if specs[0].Group != "Applications" {
		t.Fatalf("normalized group = %q, want Applications", specs[0].Group)
	}
	if specs[0].Output.ListPath != "data.apps.nodes" || strings.Join(specs[0].Output.DefaultColumns, ",") != "id,name" {
		t.Fatalf("normalized output = %+v", specs[0].Output)
	}
}

func TestParse_DerivesRelayBodyCursorPagination(t *testing.T) {
	const sdl = `
type Query { listApps(first: Int, after: String): AppConnection! }
type AppConnection { nodes: [App!]!, pageInfo: PageInfo! }
type PageInfo { endCursor: String, hasNextPage: Boolean! }
type App { id: ID!, name: String! }
`
	cfg := &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{
			Queries: []string{"listApps"},
		},
		Output: []sourceconfig.GraphQLOutputPolicy{
			{Match: []string{"listApps"}, ListPath: "data.listApps.nodes", DefaultColumns: []string{"id", "name"}},
		},
	}
	mod, err := parseSDLWithConfig(t, sdl, cfg)
	if err != nil {
		t.Fatal(err)
	}
	op := byID(mod.Operations)["console_listApps"]
	if op.Output == nil || op.Output.Pagination == nil {
		t.Fatalf("raw output pagination = %+v", op.Output)
	}
	if got := *op.Output.Pagination; got.Strategy != "body-cursor" || got.TokenParam != "variables.after" || got.TokenField != "data.listApps.pageInfo.endCursor" || got.LimitParam != "variables.first" {
		t.Fatalf("raw pagination = %+v", got)
	}
	specs := normalize.Normalize(mod)
	if len(specs) != 1 || specs[0].Output.Pagination == nil {
		t.Fatalf("normalized pagination missing: %+v", specs)
	}
	if got := *specs[0].Output.Pagination; got.Strategy != "body-cursor" || got.TokenParam != "variables.after" || got.TokenField != "data.listApps.pageInfo.endCursor" || got.LimitParam != "variables.first" {
		t.Fatalf("normalized pagination = %+v", got)
	}
}

func TestParse_AppliesGraphQLSelectionPolicy(t *testing.T) {
	maxDepth := 1
	cfg := &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{
			Queries: []string{"apps"},
		},
		Selection: &sourceconfig.GraphQLSelectionPolicy{
			MaxDepth: &maxDepth,
			Prune:    []string{"App.name"},
		},
	}
	mod, err := parseSDLWithConfig(t, consoleSDL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	q := queryDocument(t, byID(mod.Operations)["console_apps"])
	if !strings.Contains(q, "id") {
		t.Fatalf("query should keep unpruned scalar field:\n%s", q)
	}
	for _, no := range []string{"name", "owner", "email"} {
		if strings.Contains(q, no) {
			t.Fatalf("query should not include %q under selection policy:\n%s", no, q)
		}
	}
}

func TestParse_FailsClosedOnAmbiguousGraphQLGroupPolicy(t *testing.T) {
	cfg := &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{
			Queries: []string{"apps"},
		},
		Groups: []sourceconfig.GraphQLGroupPolicy{
			{Match: []string{"app*"}, Group: "Applications"},
			{Match: []string{"apps"}, Group: "Apps"},
		},
	}
	_, err := parseSDLWithConfig(t, consoleSDL, cfg)
	if err == nil {
		t.Fatal("expected fail-closed error for ambiguous group policy")
	}
	if !strings.Contains(err.Error(), "multiple graphql.groups") {
		t.Errorf("error = %v, want to mention multiple graphql.groups rules", err)
	}
}

func TestParse_FailsClosedOnAmbiguousGraphQLOutputPolicy(t *testing.T) {
	cfg := &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{
			Queries: []string{"apps"},
		},
		Output: []sourceconfig.GraphQLOutputPolicy{
			{Match: []string{"app*"}, ListPath: "data.apps.nodes"},
			{Match: []string{"apps"}, DefaultColumns: []string{"id"}},
		},
	}
	_, err := parseSDLWithConfig(t, consoleSDL, cfg)
	if err == nil {
		t.Fatal("expected fail-closed error for ambiguous output policy")
	}
	if !strings.Contains(err.Error(), "multiple graphql.output") {
		t.Errorf("error = %v, want to mention multiple graphql.output rules", err)
	}
}

func TestParse_FailsClosedWhenNoSelectableFields(t *testing.T) {
	const sdl = `
type Query { thing: Thing }
type Thing { needsArg(x: ID!): String }
`
	_, err := parseSDL(t, sdl, []string{"thing"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed error: Thing has no auto-selectable fields")
	}
	if !strings.Contains(err.Error(), "no selectable fields") {
		t.Errorf("error = %v, want to mention no selectable fields", err)
	}
}

func TestParse_FailsClosedWhenSelectionPolicyPrunesAllFields(t *testing.T) {
	const sdl = `
type Query { app: App }
type App { id: ID! }
`
	cfg := &sourceconfig.GraphQLConfig{
		Schema: "schema.graphql",
		Expose: &sourceconfig.GraphQLExpose{
			Queries: []string{"app"},
		},
		Selection: &sourceconfig.GraphQLSelectionPolicy{Prune: []string{"App.id"}},
	}
	_, err := parseSDLWithConfig(t, sdl, cfg)
	if err == nil {
		t.Fatal("expected fail-closed error after selection policy prunes every field")
	}
	if !strings.Contains(err.Error(), "no selectable fields") {
		t.Errorf("error = %v, want to mention no selectable fields", err)
	}
}

func TestParse_FloatArgBecomesNumberVariable(t *testing.T) {
	const sdl = `
type Query { ping: String }
type Mutation { setWeight(weight: Float!): App! }
type App { id: ID! }
`
	mod, err := parseSDL(t, sdl, nil, []string{"setWeight"})
	if err != nil {
		t.Fatal(err)
	}
	specs := normalize.Normalize(mod)
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	p := specs[0].Params
	if len(p) != 1 || p[0].Name != "weight" || p[0].GoType != "float64" || !p[0].Required {
		t.Fatalf("weight variable = %+v", p)
	}
}

func TestParse_ScalarListArgsPreserveElementType(t *testing.T) {
	const sdl = `
type Query { ping: String }
type Mutation {
  setAll(ids: [Int!]!, weights: [Float!]!, flags: [Boolean!]!, tags: [String!]!): App!
}
type App { id: ID! }
`
	mod, err := parseSDL(t, sdl, nil, []string{"setAll"})
	if err != nil {
		t.Fatal(err)
	}
	specs := normalize.Normalize(mod)
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	got := map[string]string{}
	for _, p := range specs[0].Params {
		got[p.Name] = p.GoType
	}
	want := map[string]string{"ids": "[]int64", "weights": "[]float64", "flags": "[]bool", "tags": "[]string"}
	if len(got) != len(want) {
		t.Fatalf("params = %#v, want %#v", got, want)
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Errorf("%s GoType = %q, want %q", name, got[name], wantType)
		}
	}
}

func TestParse_ExpandsRequiredInputObjectLeafArgs(t *testing.T) {
	const sdl = `
input CreateAppInput { name: String!, region: String }
type Query { ping: String }
type Mutation { createApp(input: CreateAppInput!): App! }
type App { id: ID! }
`
	mod, err := parseSDL(t, sdl, nil, []string{"createApp"})
	if err != nil {
		t.Fatal(err)
	}
	create := byID(mod.Operations)["console_createApp"]
	got := map[string]rawir.RawParameter{}
	for _, p := range create.Parameters {
		got[p.Name] = p
	}
	if p := got["input.name"]; p.In != "variable" || !p.Required || p.Type != "string" {
		t.Fatalf("input.name variable = %+v", p)
	}
	if p := got["input.region"]; p.In != "variable" || p.Required || p.Type != "string" {
		t.Fatalf("input.region variable = %+v", p)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(create.RequestBody.Template), &env); err != nil {
		t.Fatal(err)
	}
	vars, _ := env["variables"].(map[string]any)
	if _, ok := vars["input"].(map[string]any); !ok {
		t.Fatalf("required input object default missing: %#v", env["variables"])
	}
	specs := normalize.Normalize(mod)
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	flags := map[string]string{}
	for _, p := range specs[0].Params {
		flags[p.Name] = p.Flag
	}
	if flags["input.name"] != "input-name" || flags["input.region"] != "input-region" {
		t.Fatalf("flags = %#v", flags)
	}
}

func TestParse_AllowsOptionalNonLeafArg(t *testing.T) {
	const sdl = `
input AppFilter { name: String }
type Query { apps(filter: AppFilter): [App!]! }
type App { id: ID! }
`
	mod, err := parseSDL(t, sdl, []string{"apps"}, nil)
	if err != nil {
		t.Fatalf("optional non-scalar argument should be allowed: %v", err)
	}
	apps := byID(mod.Operations)["console_apps"]
	if len(apps.Parameters) != 1 {
		t.Fatalf("apps params = %+v, want 1", apps.Parameters)
	}
	if p := apps.Parameters[0]; p.Name != "filter.name" || p.Required || p.Type != "string" {
		t.Fatalf("filter.name variable = %+v", p)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(apps.RequestBody.Template), &env); err != nil {
		t.Fatal(err)
	}
	if q, _ := env["query"].(string); !strings.Contains(q, "$filter: AppFilter") {
		t.Errorf("optional argument should remain query-declared: %s", q)
	}
}

func TestParse_RequiredComplexInputFieldsUseBodySchema(t *testing.T) {
	const sdl = `
input MemberInput { email: String! }
input CreateAppInput { name: String!, members: [MemberInput!]! }
type Query { ping: String }
type Mutation { createApp(input: CreateAppInput!): App! }
type App { id: ID! }
`
	mod, err := parseSDL(t, sdl, nil, []string{"createApp"})
	if err != nil {
		t.Fatalf("required complex input field should use body schema: %v", err)
	}
	create := byID(mod.Operations)["console_createApp"]
	if len(create.Parameters) != 1 {
		t.Fatalf("params = %+v, want only scalar leaf flag", create.Parameters)
	}
	if p := create.Parameters[0]; p.Name != "input.name" || !p.Required {
		t.Fatalf("input.name param = %+v", p)
	}
	schema := create.RequestBody.Schema
	if schema == nil || schema.Properties["input"] == nil {
		t.Fatalf("body schema = %+v, want input object", schema)
	}
	input := schema.Properties["input"]
	if !reflect.DeepEqual(input.Required, []string{"name", "members"}) {
		t.Fatalf("input required = %#v", input.Required)
	}
	members := input.Properties["members"]
	if members == nil || members.Type != "array" || members.Items == nil {
		t.Fatalf("members schema = %+v, want array item schema", members)
	}
	if !reflect.DeepEqual(members.Items.Required, []string{"email"}) {
		t.Fatalf("member required = %#v", members.Items.Required)
	}
}

func TestParse_PreservesGraphQLVariableCoercionSemantics(t *testing.T) {
	const sdl = `
scalar JSON
input ProfileInput {
  nickname: String
  name: String!
  aliases: [String]
  tags: [String!]
}
type Query { ping: String }
type Mutation { updateUser(id: ID, profile: ProfileInput, payload: JSON): App! }
type App { id: ID! }
`
	mod, err := parseSDL(t, sdl, nil, []string{"updateUser"})
	if err != nil {
		t.Fatal(err)
	}
	schema := byID(mod.Operations)["console_updateUser"].RequestBody.Schema
	id := schema.Properties["id"]
	if id == nil || !id.Nullable || !id.AcceptIntegerID {
		t.Fatalf("id schema = %#v, want nullable ID accepting integers", id)
	}
	payload := schema.Properties["payload"]
	if payload == nil || !payload.Nullable || payload.Type != "" {
		t.Fatalf("payload schema = %#v, want unconstrained nullable custom scalar", payload)
	}
	profile := schema.Properties["profile"]
	if profile == nil || !profile.Nullable {
		t.Fatalf("profile schema = %#v, want nullable input object", profile)
	}
	if nickname := profile.Properties["nickname"]; nickname == nil || !nickname.Nullable {
		t.Fatalf("nickname schema = %#v, want nullable string", nickname)
	}
	if name := profile.Properties["name"]; name == nil || name.Nullable {
		t.Fatalf("name schema = %#v, want non-null string", name)
	}
	aliases := profile.Properties["aliases"]
	if aliases == nil || !aliases.Nullable || !aliases.AcceptSingletonArray || aliases.Items == nil || !aliases.Items.Nullable {
		t.Fatalf("aliases schema = %#v, want nullable list with nullable items", aliases)
	}
	tags := profile.Properties["tags"]
	if tags == nil || !tags.Nullable || !tags.AcceptSingletonArray || tags.Items == nil || tags.Items.Nullable {
		t.Fatalf("tags schema = %#v, want nullable list with non-null items", tags)
	}
}

func TestParse_FailsClosedOnQueryMutationNameCollision(t *testing.T) {
	const sdl = `
type Query { app(id: ID!): App }
type Mutation { app(id: ID!): App }
type App { id: ID! }
`
	_, err := parseSDL(t, sdl, []string{"app"}, []string{"app"})
	if err == nil {
		t.Fatal("expected fail-closed error for query/mutation name collision")
	}
	if !strings.Contains(err.Error(), "collision") {
		t.Errorf("error = %v, want to mention collision", err)
	}
}

func TestParse_FailsClosedWhenPolicyMatchesNothing(t *testing.T) {
	_, err := parseSDL(t, consoleSDL, []string{"nonexistent"}, nil)
	if err == nil {
		t.Fatal("expected error when the expose policy matches no operations")
	}
	if !strings.Contains(err.Error(), "no operations matched") {
		t.Errorf("error = %v, want to mention no operations matched", err)
	}
}
