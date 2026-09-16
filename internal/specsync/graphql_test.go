package specsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"

	"github.com/lathe-cli/lathe/internal/testutil"
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
	testutil.NoError(t, os.MkdirAll(filepath.Join(work, "schema"), 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(work, rel), []byte("type Query { ping: String }\n"), 0o644))

	testutil.NoError(t, syncSource(graphqlSource(rel), work, syncDir, ""))

	got, err := os.ReadFile(filepath.Join(syncDir, rel))
	testutil.Require(t, err == nil, "schema not staged: %v", err)
	testutil.Check(t, strings.Contains(string(got), "type Query"), "staged schema content = %q", got)
}

func TestSyncGraphQL_MissingSchema(t *testing.T) {
	work := t.TempDir()
	syncDir := t.TempDir()

	err := syncSource(graphqlSource("missing.graphql"), work, syncDir, "")
	testutil.Require(t, err != nil, "expected error for missing schema file")
	testutil.Check(t, strings.Contains(err.Error(), "missing missing.graphql"), "error = %v, want to name the missing schema", err)
}

func TestSync_LocalOpenAPI3StagesWorkingTree(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "api")
	cache := filepath.Join(root, "cache")
	rel := filepath.Join("openapi", "awire.yaml")
	testutil.NoError(t, os.MkdirAll(filepath.Join(local, "openapi"), 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(local, rel), []byte("openapi: \"3.0.3\"\ninfo:\n  title: Working Tree\n  version: v0\npaths: {}\n"), 0o644))

	cfg := &sourceconfig.Config{Sources: map[string]*sourceconfig.Source{
		"awire": {
			Name:      "awire",
			LocalPath: local,
			Backend:   sourceconfig.BackendOpenAPI3,
			OpenAPI3:  &sourceconfig.OpenAPI3Config{Files: []string{rel}},
		},
	}}
	testutil.NoError(t, Sync(cfg, Options{CacheRoot: cache}))

	syncDir := filepath.Join(cache, SyncSubdir, "awire")
	got, err := os.ReadFile(filepath.Join(syncDir, rel))
	testutil.Require(t, err == nil, "local spec not staged: %v", err)
	testutil.Check(t, strings.Contains(string(got), "Working Tree"), "staged spec content = %q", got)
	state, err := LoadState(syncDir)
	testutil.Require(t, err == nil, "LoadState: %v", err)
	testutil.Check(t, state.SourceKind == SourceKindLocal && state.SyncedFrom == local && state.ResolvedSHA == "", "state = %+v, want local source from %q without sha", state, local)
}
