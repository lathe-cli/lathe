package sourceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
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
		t.Run(tc.name, func(t *testing.T) {
			err := validateRef(tc.ref)
			testutil.Require(t, err != nil, "validateRef(%q) = nil, want floating-ref error", tc.ref)
			testutil.Check(t, strings.Contains(err.Error(), "floating ref"), "error message missing 'floating ref': %v", err)
		})
	}
}

func TestLoad_RejectsFloatingPinnedTag(t *testing.T) {
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: main
    backend: swagger
    swagger:
      files: [api.json]
`
	_, err := loadSources(t, body)
	testutil.Require(t, err != nil, "Load accepted pinned_tag=main; want floating-ref rejection")
	testutil.Check(t, strings.Contains(err.Error(), "floating ref"), "error = %v, want to mention floating ref", err)
}

func TestLoad_AcceptsImmutableTag(t *testing.T) {
	body := `sources:
  demo:
    repo_url: https://example.com/repo.git
    pinned_tag: v1.2.3
    backend: swagger
    swagger:
      files: [api.json]
`
	cfg, err := loadSources(t, body)
	testutil.Require(t, err == nil, "Load: %v", err)
	testutil.Check(t, cfg.Sources["demo"].PinnedTag == "v1.2.3", "pinned_tag = %q, want v1.2.3", cfg.Sources["demo"].PinnedTag)
}

func TestLoad_RejectsTraversingSourceID(t *testing.T) {
	body := `sources:
  "../outside":
    local_path: .
    backend: openapi3
    openapi3:
      files: [api.yaml]
`
	if _, err := loadSources(t, body); err == nil || !strings.Contains(err.Error(), "source ID") {
		t.Fatalf("Load error = %v, want unsafe source ID rejection", err)
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
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	cfg, err := Load(path)
	testutil.Require(t, err == nil, "Load: %v", err)
	want, err := filepath.Abs(dir)
	testutil.Require(t, err == nil, "%v", err)
	testutil.Check(t, cfg.Sources["demo"].LocalPath == want, "local_path = %q, want %q", cfg.Sources["demo"].LocalPath, want)
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
			_, err := loadSources(t, body)
			testutil.Require(t, err != nil, "Load accepted local_path with %s", name)
			testutil.Check(t, strings.Contains(err.Error(), "local_path"), "error should mention local_path: %v", err)
		})
	}
}

func TestLoad_RejectsRemoteLookingLocalPath(t *testing.T) {
	for _, localPath := range []string{"https://example.com/repo.git", "git@example.com:repo.git"} {
		t.Run(localPath, func(t *testing.T) {
			body := `sources:
  demo:
    local_path: ` + localPath + `
    backend: openapi3
    openapi3:
      files: [api.yaml]
`
			_, err := loadSources(t, body)
			testutil.Require(t, err != nil, "Load accepted remote local_path")
			testutil.Check(t, strings.Contains(err.Error(), "local_path"), "error should mention local_path: %v", err)
		})
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
			testutil.Require(t, err != nil, "validate accepted unsafe source path; want rejection")
			testutil.Check(t, strings.Contains(err.Error(), "unsafe path"), "error = %v, want to mention unsafe path", err)
		})
	}
}

func TestLoad_AcceptsFullSHA(t *testing.T) {
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
	if _, err := loadSources(t, body); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func loadSources(t *testing.T, body string) (*Config, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sources.yaml")
	testutil.NoError(t, os.WriteFile(file, []byte(body), 0o644))
	return Load(file)
}
