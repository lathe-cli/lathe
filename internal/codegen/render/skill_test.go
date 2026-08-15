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
)

func TestBodySummary_TemplatedEnvelopeGuidesMergePath(t *testing.T) {
	got := bodySummary(&runtime.RequestBody{
		Required:  true,
		MediaType: "application/json",
		Template:  `{"query":"mutation($name:String!){createApp(name:$name){id}}","variables":{}}`,
		MergePath: "variables",
	})
	if !strings.Contains(got, "variables") || !strings.Contains(got, "--set") {
		t.Errorf("bodySummary = %q, want merge-path guidance", got)
	}
}

func TestBodySummary_PlainBodyUnchanged(t *testing.T) {
	got := bodySummary(&runtime.RequestBody{Required: true, MediaType: "application/json"})
	if want := "required; media type `application/json`"; got != want {
		t.Errorf("bodySummary = %q, want %q", got, want)
	}
}

func TestRenderSkillDirectory_GeneratesSkillStructure(t *testing.T) {
	dir := t.TempDir()
	manifest := &config.Manifest{
		CLI: config.CLIInfo{Name: "acmectl", Short: "Acme API CLI", HostEnv: "ACMECTL_HOST", ConfigDirEnv: "ACMECTL_CONFIG_DIR"},
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
				{Name: "type", Flag: "type", In: runtime.InQuery, GoType: "string", Required: true, Help: "Receiver type"},
			},
			RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json"},
			Output: runtime.OutputHints{
				ListPath:          "items",
				ResponseMediaType: "application/json",
				Pagination:        &runtime.PaginationHint{Strategy: "cursor", TokenParam: "page_token"},
				Streaming:         &runtime.StreamingHint{Strategy: "sse"},
			},
			Security: &runtime.SecurityHint{Scopes: []string{"users:write"}},
		},
		{Group: "Users", Use: "delete-user", Short: "Delete user", Method: "DELETE", PathTpl: "/users/{id}", Hidden: true},
	}
	merged := MergeOverlay(specs, map[string]overlay.Override{
		"create-user": {
			Short:         "Create a user",
			Group:         "Accounts",
			Example:       "acmectl users accounts create-user --set name=alice",
			Notes:         []string{"clusterFilter expects a cluster UUID."},
			Prerequisites: []string{"Find the cluster UUID first."},
			KnownErrors:   []overlay.KnownError{{Status: 400, Cause: "missing start/end"}},
			Params:        map[string]overlay.ParamOverride{"type": {Argument: "receiver"}},
		},
	})

	if err := RenderSkillDirectory(filepath.Join(dir, "skills", "acmectl"), manifest, []SkillModule{{
		Source: source,
		State:  &specsync.State{Source: "users", Backend: "openapi3", SyncedFrom: "v1.0.0", ResolvedSHA: "abc123"},
		Specs:  merged,
	}}); err != nil {
		t.Fatalf("RenderSkillDirectory: %v", err)
	}

	skill := readFile(t, dir, "skills/acmectl/SKILL.md")
	for _, want := range []string{
		"name: acmectl",
		"acmectl search \"<intent>\" --json",
		"acmectl commands --json",
		"acmectl commands show <path...> --json",
		"acmectl commands schema --json",
		"auth.required=true",
		"references/modules/users.md",
		"flags[].input_modes",
		"error.code",
		"exit 0",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md missing %q", want)
		}
	}

	if marker := readFile(t, dir, "skills/acmectl/"+skillOwnerFile); !strings.Contains(marker, "lathe codegen") {
		t.Fatalf("owner marker missing expected content: %s", marker)
	}

	if _, err := os.Stat(filepath.Join(dir, "skills/acmectl/references/auth.md")); !os.IsNotExist(err) {
		t.Fatalf("auth.md should not be generated, stat err = %v", err)
	}

	openai := readFile(t, dir, "skills/acmectl/agents/openai.yaml")
	if !strings.Contains(openai, "default_prompt:") || !strings.Contains(openai, "$acmectl") {
		t.Fatalf("openai.yaml missing default prompt: %s", openai)
	}

	catalog := readFile(t, dir, "skills/acmectl/references/catalog.md")
	for _, want := range []string{"## Search", "## Full Catalog", "## Command Detail", "## Sensitive Flags", "## Schema", "input_modes", "body.runtime_schema", "--<flag>-env", "--<flag>-file", "--<flag>-stdin", "--set-str", "-o json", "error.http", "pause exits zero"} {
		if !strings.Contains(catalog, want) {
			t.Errorf("catalog.md missing %q", want)
		}
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
		"argument 1 `[receiver]` or `--type` (query, required): Receiver type",
		"pagination `cursor`",
		"streaming `sse`",
		"Notes:",
		"clusterFilter expects a cluster UUID.",
		"Prerequisites:",
		"Find the cluster UUID first.",
		"Known errors:",
		"HTTP 400: missing start/end",
		"Example: `acmectl accounts create-user --set name=alice`",
	} {
		if !strings.Contains(module, want) {
			t.Errorf("users.md missing %q", want)
		}
	}
	if strings.Contains(module, "Example: `acmectl users accounts create-user") {
		t.Fatalf("module reference kept stale namespaced example:\n%s", module)
	}
	if strings.Contains(module, "delete-user") || strings.Contains(module, "Raw summary") {
		t.Fatalf("module reference leaked hidden command or raw overlay content:\n%s", module)
	}
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
		if !strings.Contains(got, want) {
			t.Fatalf("module reference missing %q\nfull output:\n%s", want, got)
		}
	}

	flat := renderModuleReference(manifest, module, true)
	for _, want := range []string{
		"- Example: `acmectl users get-user --id 123`",
		"acmectl users query-logs \\\n  --start $START --end $END -o json",
		"Command: `acmectl users create-user --file user.json -o json`",
	} {
		if !strings.Contains(flat, want) {
			t.Fatalf("flat module reference missing %q\nfull output:\n%s", want, flat)
		}
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
	if !strings.Contains(namespaced, "### `acmectl billing payment list-payments`") {
		t.Fatalf("namespaced module reference should use Cobra command name:\n%s", namespaced)
	}
	if strings.Contains(namespaced, "payment api list-payments") {
		t.Fatalf("namespaced module reference kept unnormalized group path:\n%s", namespaced)
	}

	flat := renderModuleReference(manifest, module, true)
	for _, want := range []string{
		"### `acmectl payment list-payments`",
		"- Example: `acmectl payment list-payments -o json`",
	} {
		if !strings.Contains(flat, want) {
			t.Fatalf("flat module reference missing %q\nfull output:\n%s", want, flat)
		}
	}
	if strings.Contains(flat, "payment api list-payments") {
		t.Fatalf("flat module reference kept unnormalized group path:\n%s", flat)
	}
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
		if !strings.Contains(got, want) {
			t.Fatalf("graphql module reference missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestRenderSkillDirectory_RejectsUnsafeRoot(t *testing.T) {
	err := RenderSkillDirectory("", &config.Manifest{CLI: config.CLIInfo{Name: "x"}}, nil)
	if err == nil {
		t.Fatal("expected invalid root error")
	}
}

func TestRenderSkillDirectory_RefusesExistingUnownedDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills", "acmectl")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sentinel.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RenderSkillDirectory(root, &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}, nil)
	if err == nil {
		t.Fatal("expected unowned directory error")
	}
	if !strings.Contains(err.Error(), "refusing to remove") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, dir, "skills/acmectl/sentinel.txt"); got != "keep" {
		t.Fatalf("sentinel was changed: %q", got)
	}
}

func TestRenderSkillDirectory_RefusesLegacyOwnerMarker(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills", "acmectl")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".lathe-codegen-skill"), []byte("legacy marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RenderSkillDirectory(root, &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}, nil)
	if err == nil {
		t.Fatal("expected legacy owner marker to be rejected")
	}
	if !strings.Contains(err.Error(), "refusing to remove") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, dir, "skills/acmectl/.lathe-codegen-skill"); got != "legacy marker\n" {
		t.Fatalf("legacy marker was changed: %q", got)
	}
}

func TestRenderSkillDirectory_RegeneratesOwnedDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills", "acmectl")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, skillOwnerFile), []byte("old marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RenderSkillDirectory(root, &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}, nil); err != nil {
		t.Fatalf("RenderSkillDirectory: %v", err)
	}
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
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
