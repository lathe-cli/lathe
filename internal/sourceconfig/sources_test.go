package sourceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRef_Accepts(t *testing.T) {
	cases := []string{
		"v1.2.3",
		"v0.0.0-alpha",
		"release/2026.04",
		"1234567890abcdef1234567890abcdef12345678", // 40-hex SHA
	}
	for _, ref := range cases {
		if err := validateRef(ref); err != nil {
			t.Errorf("validateRef(%q) = %v, want nil", ref, err)
		}
	}
}

func TestValidateRef_Rejects(t *testing.T) {
	cases := []struct {
		name string
		ref  string
	}{
		{"head", "HEAD"},
		{"main", "main"},
		{"master", "master"},
		{"refs-heads", "refs/heads/main"},
		{"refs-remotes", "refs/remotes/origin/main"},
		{"leading-dash", "-rf"},
		{"contains-space", "v1 .0"},
		{"contains-tab", "v1\t0"},
		{"double-dot", "v1..0"},
		{"caret", "v1^0"},
		{"tilde", "v1~1"},
		{"colon", "v1:0"},
		{"question", "v?"},
		{"asterisk", "v*"},
		{"lbracket", "v["},
		{"backslash", "v\\x"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateRef(tc.ref)
			if err == nil {
				t.Fatalf("validateRef(%q) = nil, want floating-ref error", tc.ref)
			}
			if !strings.Contains(err.Error(), "floating ref") {
				t.Errorf("error message missing 'floating ref': %v", err)
			}
		})
	}
}

func TestLoad_RejectsFloatingPinnedTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: main
    backend: swagger
    swagger:
      files: [api.json]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatalf("Load accepted pinned_tag=main; want floating-ref rejection")
	}
	if !strings.Contains(err.Error(), "floating ref") {
		t.Errorf("error = %v, want to mention floating ref", err)
	}
}

func TestLoad_AcceptsImmutableTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v1.2.3
    backend: swagger
    swagger:
      files: [api.json]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sources["demo"].PinnedTag != "v1.2.3" {
		t.Errorf("pinned_tag = %q, want v1.2.3", cfg.Sources["demo"].PinnedTag)
	}
}

func TestLoad_AcceptsLocalPathWithoutPinnedTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "specs", "sources.yaml")
	body := `sources:
  demo:
    local_path: ..
    backend: openapi3
    openapi3:
      files: [api.yaml]
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sources["demo"].LocalPath != want {
		t.Errorf("local_path = %q, want %q", cfg.Sources["demo"].LocalPath, want)
	}
}

func TestLoad_RejectsLocalPathWithGitSourceFields(t *testing.T) {
	cases := map[string]string{
		"repo_url": `sources:
  demo:
    local_path: ..
    repo_url: https://example.com/repo.git
    backend: openapi3
    openapi3:
      files: [api.yaml]
`,
		"pinned_tag": `sources:
  demo:
    local_path: ..
    pinned_tag: v1.0.0
    backend: openapi3
    openapi3:
      files: [api.yaml]
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sources.yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("seed yaml: %v", err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted local_path with %s", name)
			}
			if !strings.Contains(err.Error(), "local_path") {
				t.Errorf("error should mention local_path: %v", err)
			}
		})
	}
}

func TestLoad_RejectsRemoteLookingLocalPath(t *testing.T) {
	for _, localPath := range []string{"https://example.com/repo.git", "git@example.com:repo.git"} {
		t.Run(localPath, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sources.yaml")
			body := `sources:
  demo:
    local_path: ` + localPath + `
    backend: openapi3
    openapi3:
      files: [api.yaml]
`
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("seed yaml: %v", err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted remote local_path")
			}
			if !strings.Contains(err.Error(), "local_path") {
				t.Errorf("error should mention local_path: %v", err)
			}
		})
	}
}

func TestLoad_AcceptsOpenAPI3(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v2.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sources["demo"].Backend != BackendOpenAPI3 {
		t.Errorf("backend = %q, want openapi3", cfg.Sources["demo"].Backend)
	}
}

func TestLoad_RejectsOpenAPI3WithoutFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v2.0.0
    backend: openapi3
    openapi3:
      files: []
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted openapi3 with empty files; want rejection")
	}
	if !strings.Contains(err.Error(), "non-empty openapi3.files") {
		t.Errorf("error = %v, want to mention non-empty openapi3.files", err)
	}
}

func TestValidate_RejectsUnsafeSourcePaths(t *testing.T) {
	remote := func(src Source) Source {
		src.RepoURL = "https://example.com/repo.git"
		src.PinnedTag = "v2.0.0"
		return src
	}
	cases := []struct {
		name string
		src  Source
	}{
		{
			name: "swagger absolute file",
			src:  remote(Source{Backend: BackendSwagger, Swagger: &SwaggerConfig{Files: []string{"/tmp/api.json"}}}),
		},
		{
			name: "openapi empty segment",
			src:  remote(Source{Backend: BackendOpenAPI3, OpenAPI3: &OpenAPI3Config{Files: []string{"api//openapi.yaml"}}}),
		},
		{
			name: "proto staging from traversal",
			src: remote(Source{
				Backend: BackendProto,
				Proto: &ProtoConfig{
					Staging: []StagingEntry{{From: "../proto", To: "proto"}},
					Entries: []string{"api/v1/service.proto"},
				},
			}),
		},
		{
			name: "proto staging to absolute",
			src: remote(Source{
				Backend: BackendProto,
				Proto: &ProtoConfig{
					Staging: []StagingEntry{{From: "proto", To: "/tmp/proto"}},
					Entries: []string{"api/v1/service.proto"},
				},
			}),
		},
		{
			name: "proto entry traversal",
			src: remote(Source{
				Backend: BackendProto,
				Proto: &ProtoConfig{
					Staging: []StagingEntry{{From: "proto", To: "proto"}},
					Entries: []string{"../api/v1/service.proto"},
				},
			}),
		},
		{
			name: "proto import root traversal",
			src: remote(Source{
				Backend: BackendProto,
				Proto: &ProtoConfig{
					Staging:     []StagingEntry{{From: "proto", To: "proto"}},
					Entries:     []string{"api/v1/service.proto"},
					ImportRoots: []string{"../includes"},
				},
			}),
		},
		{
			name: "graphql schema traversal",
			src: remote(Source{
				Backend: BackendGraphQL,
				GraphQL: &GraphQLConfig{
					Schema: "../schema.graphql",
					Expose: &GraphQLExpose{
						Queries: []string{"viewer"},
					},
				},
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(&tc.src, t.TempDir())
			if err == nil {
				t.Fatal("validate accepted unsafe source path; want rejection")
			}
			if !strings.Contains(err.Error(), "unsafe path") {
				t.Errorf("error = %v, want to mention unsafe path", err)
			}
		})
	}
}

func TestLoad_RejectsOpenAPI3WithSwaggerBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
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
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted openapi3 with swagger block; want rejection")
	}
	if !strings.Contains(err.Error(), "must not set swagger block") {
		t.Errorf("error = %v, want to mention swagger block", err)
	}
}

func TestLoad_AcceptsGraphQL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
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
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	src := cfg.Sources["console"]
	if src.Backend != BackendGraphQL {
		t.Errorf("backend = %q, want graphql", src.Backend)
	}
	if src.GraphQL == nil || src.GraphQL.Schema != "schema.graphql" {
		t.Fatalf("graphql config = %+v", src.GraphQL)
	}
	if src.GraphQL.Expose == nil || len(src.GraphQL.Expose.Queries) != 2 || len(src.GraphQL.Expose.Mutations) != 1 {
		t.Fatalf("expose = %+v", src.GraphQL.Expose)
	}
	if len(src.GraphQL.Groups) != 1 || src.GraphQL.Groups[0].Group != "Applications" {
		t.Fatalf("groups = %+v", src.GraphQL.Groups)
	}
	if len(src.GraphQL.Output) != 1 || src.GraphQL.Output[0].ListPath != "data.apps.nodes" || len(src.GraphQL.Output[0].DefaultColumns) != 2 {
		t.Fatalf("output = %+v", src.GraphQL.Output)
	}
	if src.GraphQL.Selection == nil || src.GraphQL.Selection.MaxDepth == nil || *src.GraphQL.Selection.MaxDepth != 2 || len(src.GraphQL.Selection.Prune) != 1 {
		t.Fatalf("selection = %+v", src.GraphQL.Selection)
	}
}

func TestLoad_RejectsGraphQLWithoutSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      expose:
        queries: ["apps"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted graphql without schema; want rejection")
	}
	if !strings.Contains(err.Error(), "graphql.schema") {
		t.Errorf("error = %v, want to mention graphql.schema", err)
	}
}

func TestLoad_RejectsGraphQLWithoutExposePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  console:
    repo_url: https://example.com/repo.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema.graphql
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted graphql without expose policy; want fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "refusing to expose the whole schema") {
		t.Errorf("error = %v, want fail-closed exposure rejection", err)
	}
}

func TestLoad_RejectsGraphQLWithSwaggerBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
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
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted graphql with swagger block; want rejection")
	}
	if !strings.Contains(err.Error(), "must not set swagger block") {
		t.Errorf("error = %v, want to mention swagger block", err)
	}
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sources.yaml")
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
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("seed yaml: %v", err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted invalid graphql policy %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoad_AcceptsFullSHA(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: 1234567890abcdef1234567890abcdef12345678
    backend: proto
    proto:
      staging:
        - from: ./api
          to: api
      entries: [api/v1/service.proto]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
