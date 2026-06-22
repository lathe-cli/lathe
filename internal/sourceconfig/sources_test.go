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
