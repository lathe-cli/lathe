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
			Group:       "Users",
			Use:         "create-user",
			Short:       "Raw summary",
			Method:      "POST",
			PathTpl:     "/users",
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
		"create-user": {Short: "Create a user", Group: "Accounts", Example: "acmectl users accounts create-user --set name=alice"},
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
	for _, want := range []string{"## Search", "## Full Catalog", "## Command Detail", "## Schema", "--set-str", "-o json"} {
		if !strings.Contains(catalog, want) {
			t.Errorf("catalog.md missing %q", want)
		}
	}

	module := readFile(t, dir, "skills/acmectl/references/modules/users.md")
	for _, want := range []string{
		"Repository: https://example.com/acme.git",
		"Resolved SHA: `abc123`",
		"## Accounts",
		"`acmectl users accounts create-user`",
		"Summary: Create a user",
		"Auth: required; scopes: `users:write`",
		"Body: required; media type `application/json`",
		"pagination `cursor`",
		"streaming `sse`",
		"Example: `acmectl users accounts create-user --set name=alice`",
	} {
		if !strings.Contains(module, want) {
			t.Errorf("users.md missing %q", want)
		}
	}
	if strings.Contains(module, "delete-user") || strings.Contains(module, "Raw summary") {
		t.Fatalf("module reference leaked hidden command or raw overlay content:\n%s", module)
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
