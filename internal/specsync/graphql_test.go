package specsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func graphqlSource(schema string) *sourceconfig.Source {
	return &sourceconfig.Source{
		Name:      "console",
		PinnedTag: "v1.0.0",
		Backend:   sourceconfig.BackendGraphQL,
		GraphQL: &sourceconfig.GraphQLConfig{
			Schema: schema,
			Expose: &sourceconfig.GraphQLExpose{Queries: []string{"ping"}},
		},
	}
}

func TestSyncGraphQL_StagesSchema(t *testing.T) {
	work := t.TempDir()
	syncDir := t.TempDir()
	rel := filepath.Join("schema", "console.graphql")
	if err := os.MkdirAll(filepath.Join(work, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, rel), []byte("type Query { ping: String }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncGraphQL(graphqlSource(rel), work, syncDir); err != nil {
		t.Fatalf("syncGraphQL: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(syncDir, rel))
	if err != nil {
		t.Fatalf("schema not staged: %v", err)
	}
	if !strings.Contains(string(got), "type Query") {
		t.Errorf("staged schema content = %q", got)
	}
}

func TestSyncGraphQL_MissingSchema(t *testing.T) {
	work := t.TempDir()
	syncDir := t.TempDir()

	err := syncGraphQL(graphqlSource("missing.graphql"), work, syncDir)
	if err == nil {
		t.Fatal("expected error for missing schema file")
	}
	if !strings.Contains(err.Error(), "missing missing.graphql") {
		t.Errorf("error = %v, want to name the missing schema", err)
	}
}

func TestSync_LocalOpenAPI3StagesWorkingTree(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "api")
	cache := filepath.Join(root, "cache")
	rel := filepath.Join("openapi", "awire.yaml")
	if err := os.MkdirAll(filepath.Join(local, "openapi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, rel), []byte("openapi: \"3.0.3\"\ninfo:\n  title: Working Tree\n  version: v0\npaths: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &sourceconfig.Config{Sources: map[string]*sourceconfig.Source{
		"awire": {
			Name:      "awire",
			LocalPath: local,
			Backend:   sourceconfig.BackendOpenAPI3,
			OpenAPI3:  &sourceconfig.OpenAPI3Config{Files: []string{rel}},
		},
	}}
	if err := Sync(cfg, Options{CacheRoot: cache}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	syncDir := filepath.Join(cache, SyncSubdir, "awire")
	got, err := os.ReadFile(filepath.Join(syncDir, rel))
	if err != nil {
		t.Fatalf("local spec not staged: %v", err)
	}
	if !strings.Contains(string(got), "Working Tree") {
		t.Errorf("staged spec content = %q", got)
	}
	state, err := LoadState(syncDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.SourceKind != SourceKindLocal || state.SyncedFrom != local || state.ResolvedSHA != "" {
		t.Errorf("state = %+v, want local source from %q without sha", state, local)
	}
}
