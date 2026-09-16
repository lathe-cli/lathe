package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/specsync"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBodySummary_TemplatedEnvelopeGuidesMergePath(t *testing.T) {
	got := bodySummary(&runtime.RequestBody{
		Required:  true,
		MediaType: "application/json",
		Template:  `{"query":"mutation($name:String!){createApp(name:$name){id}}","variables":{}}`,
		MergePath: "variables",
	})
	testutil.Check(t, strings.Contains(got, "variables") && strings.Contains(got, "--set"), "bodySummary = %q, want merge-path guidance", got)
}

func TestBodySummary_PlainBodyUnchanged(t *testing.T) {
	got := bodySummary(&runtime.RequestBody{Required: true, MediaType: "application/json"})
	if want := "required; media type `application/json`"; got != want {
		t.Errorf("bodySummary = %q, want %q", got, want)
	}
}

func TestRenderSkillDirectory_GeneratesSkillStructure(t *testing.T) {
	dir := t.TempDir()
	unsafeParamName := "type`\n**INJECT**"
	manifest := &config.Manifest{
		CLI:      config.CLIInfo{Name: "acmectl", Short: "Acme API CLI", HostEnv: "ACMECTL_HOST", ConfigDirEnv: "ACMECTL_CONFIG_DIR"},
		Contexts: map[string]config.ContextInfo{"organization": {Env: "ACMECTL_ORG_ID"}},
	}
	source := &sourceconfig.Source{
		Name:      "users",
		RepoURL:   "https://example.com/acme.git",
		PinnedTag: "v1.0.0",
		Backend:   sourceconfig.BackendOpenAPI3,
		OpenAPI3:  &sourceconfig.OpenAPI3Config{Files: []string{"openapi.yaml"}},
	}
	specs := []runtime.CommandSpec{
		{
			Group:   "Users",
			Use:     "create-user",
			Short:   "Raw summary",
			Method:  "POST",
			PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: unsafeParamName, Flag: "type", In: runtime.InQuery, GoType: "string", Required: true, Help: "Receiver type", Context: "organization"},
			},
			RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json"},
			Output: runtime.OutputHints{
				ListPath:          "items",
				ResponseMediaType: "application/json",
				Pagination:        &runtime.PaginationHint{Strategy: "cursor", TokenParam: "page_token"},
				Streaming:         &runtime.StreamingHint{Strategy: "sse"},
			},
			Security:   &runtime.SecurityHint{Scopes: []string{"users:write"}},
			SetContext: &runtime.ContextSetHint{Name: "organization", Param: unsafeParamName},
		},
		{Group: "Users", Use: "delete-user", Short: "Delete user", Method: "DELETE", PathTpl: "/users/{id}", Hidden: true},
	}
	merged := mustMergeOverlay(t, specs, map[string]overlay.Override{
		"create-user": {
			Short:         "Create a user",
			Group:         "Accounts",
			Example:       "acmectl users accounts create-user --set name=alice",
			Notes:         []string{"clusterFilter expects a cluster UUID."},
			Prerequisites: []string{"Find the cluster UUID first."},
			KnownErrors:   []overlay.KnownError{{Status: 400, Cause: "missing start/end"}},
			SearchTerms:   []string{"member", "invite"},
			Params:        map[string]overlay.ParamOverride{unsafeParamName: {Argument: "receiver"}},
		},
	})

	testutil.NoError(t, RenderSkillDirectory(filepath.Join(dir, "skills", "acmectl"), manifest, []SkillModule{{
		Source: source,
		State:  &specsync.State{Source: "users", Backend: "openapi3", SyncedFrom: "v1.0.0", ResolvedSHA: "abc123"},
		Specs:  merged,
	}}))

	skill := readFile(t, dir, "skills/acmectl/SKILL.md")
	for _, want := range []string{
		"name: acmectl",
		"acmectl search \"<intent>\" --json",
		"acmectl commands --json",
		"acmectl commands show <path...> --json",
		"acmectl commands schema --json",
		"auth.required=true",
		"mutation",
		"dry_run",
		"not `read`",
		"explicit user confirmation",
		"references/modules/users.md",
		"flags[].input_modes",
		"error.code",
		"exit 0",
		"auth context status -o json",
	} {
		testutil.Check(t, strings.Contains(skill, want), "SKILL.md missing %q", want)
	}

	if marker := readFile(t, dir, "skills/acmectl/"+skillOwnerFile); !strings.Contains(marker, "lathe codegen") {
		t.Fatalf("owner marker missing expected content: %s", marker)
	}

	if _, err := os.Stat(filepath.Join(dir, "skills/acmectl/references/auth.md")); !os.IsNotExist(err) {
		t.Fatalf("auth.md should not be generated, stat err = %v", err)
	}

	openai := readFile(t, dir, "skills/acmectl/agents/openai.yaml")
	testutil.Require(t, strings.Contains(openai, "default_prompt:") && strings.Contains(openai, "$acmectl"), "openai.yaml missing default prompt: %s", openai)

	catalog := readFile(t, dir, "skills/acmectl/references/catalog.md")
	for _, want := range []string{"## Search", "## Full Catalog", "## Command Detail", "## Sensitive Flags", "## Schema", "input_modes", "body.runtime_schema", "--<flag>-env", "--<flag>-file", "--<flag>-stdin", "--set-str", "-o json", "error.http", "pause exits zero", "`mutation`", "`dry_run`", "catalog_schema_version", "surfaces", "other than `read`", "explicit user confirmation"} {
		testutil.Check(t, strings.Contains(catalog, want), "catalog.md missing %q", want)
	}

	module := readFile(t, dir, "skills/acmectl/references/modules/users.md")
	for _, want := range []string{
		"Repository: https://example.com/acme.git",
		"Resolved SHA: `abc123`",
		"## Accounts",
		"`acmectl accounts create-user`",
		"Summary: Create a user",
		"Auth: required; scopes: `users:write`",
		"Body: required; media type `application/json`",
		"argument 1 `[receiver]` or `--type` (query, required, context `organization` via `ACMECTL_ORG_ID`): Receiver type",
		"pagination `cursor`",
		"streaming `sse`",
		"Notes:",
		"clusterFilter expects a cluster UUID.",
		"Prerequisites:",
		"Find the cluster UUID first.",
		"Known errors:",
		"HTTP 400: missing start/end",
		"Search terms: `member`, `invite`",
		"context `organization` via `ACMECTL_ORG_ID`",
		"Sets context `organization` from parameter `type` after success.",
		"Example: `acmectl accounts create-user --set name=alice`",
	} {
		testutil.Check(t, strings.Contains(module, want), "users.md missing %q", want)
	}
	testutil.Require(t, !strings.Contains(module, "Example: `acmectl users accounts create-user"), "module reference kept stale namespaced example:\n%s", module)
	testutil.Require(t, !strings.Contains(module, "delete-user") && !strings.Contains(module, "Raw summary"), "module reference leaked hidden command or raw overlay content:\n%s", module)
	testutil.Require(t, !strings.Contains(module, "**INJECT**"), "module reference contains injected parameter content:\n%s", module)
}

func TestRenderModuleReference_FormatsExamples(t *testing.T) {
	manifest := &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}
	module := SkillModule{
		Source: &sourceconfig.Source{Name: "users"},
		Specs: []runtime.CommandSpec{
			{
				Group:   "Users",
				Use:     "get-user",
				Short:   "Get user",
				Method:  "GET",
				PathTpl: "/users/{id}",
				Example: "acmectl users users get-user --id 123",
			},
			{
				Group:   "Users",
				Use:     "query-logs",
				Short:   "Query logs",
				Method:  "POST",
				PathTpl: "/logs/query",
				Example: "END=$(date +%s); START=$((END - 3600))\n" +
					"acmectl users users query-logs \\\n" +
					"  --start $START --end $END -o json\n" +
					"jq '.items[]'",
			},
			{
				Group:   "Users",
				Use:     "create-user",
				Short:   "Create user",
				Method:  "POST",
				PathTpl: "/users",
				Examples: []runtime.CommandExample{{
					Summary:          "Create from JSON",
					Command:          "acmectl users users create-user --file user.json -o json",
					BodyShape:        []byte(`{"input":{"name":"..."}}`),
					OutputHints:      &runtime.ExampleOutputHints{IDPath: "data.createUser.id"},
					FollowUpCommands: []string{"acmectl users users get-user --id <id> -o json"},
				}},
			},
		},
	}

	got := renderModuleReference(manifest, module, false)
	for _, want := range []string{
		"- Example: `acmectl users users get-user --id 123`",
		"- Example:\n\n```\nEND=$(date +%s); START=$((END - 3600))\nacmectl users users query-logs \\\n  --start $START --end $END -o json\njq '.items[]'\n```",
		"- Examples:\n  - Create from JSON\n    Command: `acmectl users users create-user --file user.json -o json`\n    Body shape: `{\"input\":{\"name\":\"...\"}}`\n    Output ID path: `data.createUser.id`\n    Follow-up commands:\n      - `acmectl users users get-user --id <id> -o json`",
	} {
		testutil.Require(t, strings.Contains(got, want), "module reference missing %q\nfull output:\n%s", want, got)
	}

	flat := renderModuleReference(manifest, module, true)
	for _, want := range []string{
		"- Example: `acmectl users get-user --id 123`",
		"acmectl users query-logs \\\n  --start $START --end $END -o json",
		"Command: `acmectl users create-user --file user.json -o json`",
	} {
		testutil.Require(t, strings.Contains(flat, want), "flat module reference missing %q\nfull output:\n%s", want, flat)
	}
}

func TestRenderModuleReference_NormalizesMultiWordGroupPaths(t *testing.T) {
	manifest := &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}
	module := SkillModule{
		Source: &sourceconfig.Source{Name: "billing"},
		Specs: []runtime.CommandSpec{{
			Group:   "Payment API",
			Use:     "list-payments",
			Short:   "List payments",
			Method:  "GET",
			PathTpl: "/payments",
			Example: "acmectl billing payment api list-payments -o json",
		}},
	}

	namespaced := renderModuleReference(manifest, module, false)
	testutil.Require(t, strings.Contains(namespaced, "### `acmectl billing payment list-payments`"), "namespaced module reference should use Cobra command name:\n%s", namespaced)
	testutil.Require(t, !strings.Contains(namespaced, "payment api list-payments"), "namespaced module reference kept unnormalized group path:\n%s", namespaced)

	flat := renderModuleReference(manifest, module, true)
	for _, want := range []string{
		"### `acmectl payment list-payments`",
		"- Example: `acmectl payment list-payments -o json`",
	} {
		testutil.Require(t, strings.Contains(flat, want), "flat module reference missing %q\nfull output:\n%s", want, flat)
	}
	testutil.Require(t, !strings.Contains(flat, "payment api list-payments"), "flat module reference kept unnormalized group path:\n%s", flat)
}

func TestRenderModuleReference_GraphQLSourceSummary(t *testing.T) {
	manifest := &config.Manifest{CLI: config.CLIInfo{Name: "consolectl"}}
	maxDepth := 2
	module := SkillModule{
		Source: &sourceconfig.Source{
			Name:      "console",
			RepoURL:   "https://example.com/console.git",
			PinnedTag: "v3.0.0",
			Backend:   sourceconfig.BackendGraphQL,
			GraphQL: &sourceconfig.GraphQLConfig{
				Schema: "schema.graphql",
				Expose: &sourceconfig.GraphQLExpose{
					Queries:   []string{"app*"},
					Mutations: []string{"createApp"},
				},
				Groups: []sourceconfig.GraphQLGroupPolicy{
					{Match: []string{"app*"}, Group: "Applications"},
				},
				Output: []sourceconfig.GraphQLOutputPolicy{
					{Match: []string{"apps"}, ListPath: "data.apps.nodes", DefaultColumns: []string{"id", "name"}},
				},
				Selection: &sourceconfig.GraphQLSelectionPolicy{
					MaxDepth: &maxDepth,
					Prune:    []string{"App.secret"},
				},
			},
		},
		Specs: []runtime.CommandSpec{{
			Group:   "Applications",
			Use:     "apps",
			Short:   "List apps",
			Method:  "POST",
			PathTpl: "/graphql",
			RequestBody: &runtime.RequestBody{
				Required:  true,
				MediaType: "application/json",
				Template:  `{"query":"query apps { apps { id } }","variables":{}}`,
				MergePath: "variables",
			},
			Output: runtime.OutputHints{
				ListPath:       "data.apps.nodes",
				DefaultColumns: []string{"id", "name"},
			},
		}},
	}

	got := renderModuleReference(manifest, module, true)
	for _, want := range []string{
		"Backend: `graphql`",
		"Schema: `schema.graphql`",
		"Expose queries: `app*`",
		"Expose mutations: `createApp`",
		"Group policies: `1`",
		"Output policies: `1`",
		"Selection policy: max depth `2`; prune rules `1`",
		"Body: required; templated body, set inputs under `variables`",
		"Output: list path `data.apps.nodes`; columns `id`, `name`",
	} {
		testutil.Require(t, strings.Contains(got, want), "graphql module reference missing %q\nfull output:\n%s", want, got)
	}
}

func TestRenderSkillDirectory_RejectsUnsafeRoot(t *testing.T) {
	err := RenderSkillDirectory("", &config.Manifest{CLI: config.CLIInfo{Name: "x"}}, nil)
	testutil.Require(t, err != nil, "expected invalid root error")
}

func TestRenderSkillDirectory_RefusesExistingUnownedDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills", "acmectl")
	testutil.NoError(t, os.MkdirAll(root, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(root, "sentinel.txt"), []byte("keep"), 0o644))

	err := RenderSkillDirectory(root, &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}, nil)
	testutil.Require(t, err != nil, "expected unowned directory error")
	testutil.Require(t, strings.Contains(err.Error(), "refusing to remove"), "unexpected error: %v", err)
	if got := readFile(t, dir, "skills/acmectl/sentinel.txt"); got != "keep" {
		t.Fatalf("sentinel was changed: %q", got)
	}
}

func TestRenderSkillDirectory_RefusesLegacyOwnerMarker(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills", "acmectl")
	testutil.NoError(t, os.MkdirAll(root, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(root, ".lathe-codegen-skill"), []byte("legacy marker\n"), 0o644))

	err := RenderSkillDirectory(root, &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}, nil)
	testutil.Require(t, err != nil, "expected legacy owner marker to be rejected")
	testutil.Require(t, strings.Contains(err.Error(), "refusing to remove"), "unexpected error: %v", err)
	if got := readFile(t, dir, "skills/acmectl/.lathe-codegen-skill"); got != "legacy marker\n" {
		t.Fatalf("legacy marker was changed: %q", got)
	}
}

func TestRenderSkillDirectory_RegeneratesOwnedDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills", "acmectl")
	testutil.NoError(t, os.MkdirAll(root, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(root, skillOwnerFile), []byte("old marker"), 0o644))
	testutil.NoError(t, os.WriteFile(filepath.Join(root, "stale.txt"), []byte("stale"), 0o644))

	testutil.NoError(t, RenderSkillDirectory(root, &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}, nil))
	if _, err := os.Stat(filepath.Join(root, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale file should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, skillOwnerFile)); err != nil {
		t.Fatalf("owner marker should be regenerated: %v", err)
	}
}

func readFile(t *testing.T, root string, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	testutil.Require(t, err == nil, "read %s: %v", path, err)
	return string(data)
}
