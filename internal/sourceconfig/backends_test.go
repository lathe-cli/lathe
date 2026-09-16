package sourceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestLoad_AcceptsOpenAPI3(t *testing.T) {
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v2.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
      expose:
        operation_ids: [Pet_List, Pet_Get]
`
	cfg, err := loadSources(t, body)
	testutil.Require(t, err == nil, "Load: %v", err)
	testutil.Check(t, cfg.Sources["demo"].Backend == BackendOpenAPI3, "backend = %q, want openapi3", cfg.Sources["demo"].Backend)
	if got := cfg.Sources["demo"].OpenAPI3.Expose.OperationIDs; len(got) != 2 || got[0] != "Pet_List" || got[1] != "Pet_Get" {
		t.Fatalf("operation_ids = %#v", got)
	}
}

func TestLoad_RejectsInvalidOpenAPI3Expose(t *testing.T) {
	for name, operationIDs := range map[string]string{
		"empty":     "[]",
		"blank":     "['']",
		"duplicate": "[Pet_List, Pet_List]",
	} {
		t.Run(name, func(t *testing.T) {
			body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v2.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
      expose:
        operation_ids: ` + operationIDs + "\n"
			if _, err := loadSources(t, body); err == nil || !strings.Contains(err.Error(), "openapi3.expose") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoad_RejectsOpenAPI3WithoutFiles(t *testing.T) {
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v2.0.0
    backend: openapi3
    openapi3:
      files: []
`
	_, err := loadSources(t, body)
	testutil.Require(t, err != nil, "Load accepted openapi3 with empty files; want rejection")
	testutil.Check(t, strings.Contains(err.Error(), "non-empty openapi3.files"), "error = %v, want to mention non-empty openapi3.files", err)
}

func TestLoad_RejectsOpenAPI3WithSwaggerBlock(t *testing.T) {
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v2.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
    swagger:
      files: [api.json]
`
	_, err := loadSources(t, body)
	testutil.Require(t, err != nil, "Load accepted openapi3 with swagger block; want rejection")
	testutil.Check(t, strings.Contains(err.Error(), "must not set swagger block"), "error = %v, want to mention swagger block", err)
}

func TestLoad_AcceptsGraphQL(t *testing.T) {
	body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema.graphql
      expose:
        queries: ["apps", "app"]
        mutations: ["createApp"]
      groups:
        - match: ["app*"]
          group: Applications
      output:
        - match: ["apps"]
          list_path: data.apps.nodes
          default_columns: ["id", "name"]
      selection:
        max_depth: 2
        prune: ["App.secret"]
`
	cfg, err := loadSources(t, body)
	testutil.Require(t, err == nil, "Load: %v", err)
	src := cfg.Sources["console"]
	testutil.Check(t, src.Backend == BackendGraphQL, "backend = %q, want graphql", src.Backend)
	testutil.Require(t, src.GraphQL != nil && src.GraphQL.Schema == "schema.graphql", "graphql config = %+v", src.GraphQL)
	testutil.Require(t, src.GraphQL.Expose != nil && len(src.GraphQL.Expose.Queries) == 2 && len(src.GraphQL.Expose.Mutations) == 1, "expose = %+v", src.GraphQL.Expose)
	testutil.Require(t, len(src.GraphQL.Groups) == 1 && src.GraphQL.Groups[0].Group == "Applications", "groups = %+v", src.GraphQL.Groups)
	testutil.Require(t, len(src.GraphQL.Output) == 1 && src.GraphQL.Output[0].ListPath == "data.apps.nodes" && len(src.GraphQL.Output[0].DefaultColumns) == 2, "output = %+v", src.GraphQL.Output)
	testutil.Require(t, src.GraphQL.Selection != nil && src.GraphQL.Selection.MaxDepth != nil && *src.GraphQL.Selection.MaxDepth == 2 && len(src.GraphQL.Selection.Prune) == 1, "selection = %+v", src.GraphQL.Selection)
}

func TestLoad_RejectsGraphQLWithoutSchema(t *testing.T) {
	body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      expose:
        queries: ["apps"]
`
	_, err := loadSources(t, body)
	testutil.Require(t, err != nil, "Load accepted graphql without schema; want rejection")
	testutil.Check(t, strings.Contains(err.Error(), "graphql.schema"), "error = %v, want to mention graphql.schema", err)
}

func TestLoad_RejectsGraphQLWithoutExposePolicy(t *testing.T) {
	body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema.graphql
`
	_, err := loadSources(t, body)
	testutil.Require(t, err != nil, "Load accepted graphql without expose policy; want fail-closed rejection")
	testutil.Check(t, strings.Contains(err.Error(), "refusing to expose the whole schema"), "error = %v, want fail-closed exposure rejection", err)
}

func TestLoad_RejectsGraphQLWithSwaggerBlock(t *testing.T) {
	body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema.graphql
      expose:
        queries: ["apps"]
    swagger:
      files: [api.json]
`
	_, err := loadSources(t, body)
	testutil.Require(t, err != nil, "Load accepted graphql with swagger block; want rejection")
	testutil.Check(t, strings.Contains(err.Error(), "must not set swagger block"), "error = %v, want to mention swagger block", err)
}

func TestLoad_RejectsInvalidGraphQLPolicy(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		want   string
	}{
		{
			name: "group missing match",
			policy: `      groups:
        - group: Applications
`,
			want: "requires non-empty match",
		},
		{
			name: "group missing group",
			policy: `      groups:
        - match: ["apps"]
`,
			want: "requires group",
		},
		{
			name: "output missing shape",
			policy: `      output:
        - match: ["apps"]
`,
			want: "requires list_path or default_columns",
		},
		{
			name: "output invalid list path",
			policy: `      output:
        - match: ["apps"]
          list_path: data..nodes
`,
			want: "empty path segment",
		},
		{
			name: "output invalid default column",
			policy: `      output:
        - match: ["apps"]
          default_columns: [""]
`,
			want: "empty path segment",
		},
		{
			name: "selection negative depth",
			policy: `      selection:
        max_depth: -1
`,
			want: "must be > 0",
		},
		{
			name: "selection zero depth",
			policy: `      selection:
        max_depth: 0
`,
			want: "must be > 0",
		},
		{
			name: "selection prune missing type",
			policy: `      selection:
        prune: ["owner"]
`,
			want: "must be Type.field",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema.graphql
      expose:
        queries: ["apps"]
` + tc.policy
			_, err := loadSources(t, body)
			testutil.Require(t, err != nil, "Load accepted invalid graphql policy %q", tc.name)
			testutil.Check(t, strings.Contains(err.Error(), tc.want), "error = %v, want %q", err, tc.want)
		})
	}
}

func TestLoad_RejectsProtoEntryThatLooksLikeProtocOption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  demo:
    local_path: .
    backend: proto
    proto:
      staging:
        - from: .
          to: api
      entries: ["--descriptor_set_out=/tmp/out.pb"]
`
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "proto.entries") {
		t.Fatalf("Load error = %v, want protoc option rejection", err)
	}

	body = strings.Replace(body, "--descriptor_set_out=/tmp/out.pb", "@args.proto", 1)
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "proto.entries") {
		t.Fatalf("Load error = %v, want protoc response file rejection", err)
	}
}

func TestLoad_AcceptsProtoDependencies(t *testing.T) {
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: 1234567890abcdef1234567890abcdef12345678
    backend: proto
    proto:
      staging:
        - from: .
          to: example.com/demo
      entries: [example.com/demo/api/service.proto]
      import_roots: [example.com/demo]
      dependencies:
        - kind: buf
          module: buf.build/googleapis/googleapis
          commit: 004180b77378443887d3b55cabc00384
          digest: b5:e8f475fe3330f31f5fd86ac689093bcd274e19611a09db91f41d637cb9197881ce89882b94d13a58738e53c91c6e4bae7dc1feba85f590164c975a89e25115dc
          staging:
            - from: .
              to: .
        - kind: go_module
          module: k8s.io/api
          version: v0.35.4
          sum: h1:abcdefghijklmnopqrstuvwxyz
          staging:
            - from: .
              to: k8s.io/api
        - kind: git
          repo_url: https://github.com/argoproj/argo-events
          pinned_tag: 1234567890abcdef1234567890abcdef12345678
          staging:
            - from: .
              to: github.com/argoproj/argo-events
`
	cfg, err := loadSources(t, body)
	testutil.Require(t, err == nil, "Load: %v", err)
	if got := len(cfg.Sources["demo"].Proto.Dependencies); got != 3 {
		t.Fatalf("dependency count = %d, want 3", got)
	}
}

func TestValidateProtoDependenciesRejectsIncompletePins(t *testing.T) {
	staging := []StagingEntry{{From: ".", To: "."}}
	for _, tc := range []struct {
		name string
		dep  ProtoDependency
	}{
		{name: "empty buf digest", dep: ProtoDependency{Kind: ProtoDependencyBuf, Module: "buf.build/acme/api", Commit: "004180b77378443887d3b55cabc00384", Digest: "b5:", Staging: staging}},
		{name: "v1 lock digest", dep: ProtoDependency{Kind: ProtoDependencyBuf, Module: "buf.build/acme/api", Commit: "004180b77378443887d3b55cabc00384", Digest: "shake256:c62ecead9b13485a02893cd678a6c81e40879bf00ea509bbc6fd8f1b2cc33ecc", Staging: staging}},
		{name: "empty go sum", dep: ProtoDependency{Kind: ProtoDependencyGoModule, Module: "example.com/api", Version: "v1.0.0", Sum: "h1:", Staging: staging}},
		{name: "unsafe module", dep: ProtoDependency{Kind: ProtoDependencyGoModule, Module: "../api", Version: "v1.0.0", Sum: "h1:sum", Staging: staging}},
		{name: "unknown kind", dep: ProtoDependency{Kind: "http", Staging: staging}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateProtoDependencies([]ProtoDependency{tc.dep}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
