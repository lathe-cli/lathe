package sourceconfig

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	latheconfig "github.com/lathe-cli/lathe/pkg/config"
	"gopkg.in/yaml.v3"
)

const (
	BackendSwagger  = "swagger"
	BackendProto    = "proto"
	BackendOpenAPI3 = "openapi3"
	BackendGraphQL  = "graphql"
)

type Config struct {
	Sources map[string]*Source `yaml:"sources"`
}

type Source struct {
	Name            string          `yaml:"-"`
	DisplayName     string          `yaml:"display_name,omitempty"`
	DefaultHostname *string         `yaml:"default_hostname,omitempty"`
	RepoURL         string          `yaml:"repo_url"`
	PinnedTag       string          `yaml:"pinned_tag"`
	LocalPath       string          `yaml:"local_path"`
	Backend         string          `yaml:"backend"`
	Swagger         *SwaggerConfig  `yaml:"swagger,omitempty"`
	Proto           *ProtoConfig    `yaml:"proto,omitempty"`
	OpenAPI3        *OpenAPI3Config `yaml:"openapi3,omitempty"`
	GraphQL         *GraphQLConfig  `yaml:"graphql,omitempty"`
}

type SwaggerConfig struct {
	Files []string `yaml:"files"`
}

type ProtoConfig struct {
	Staging     []StagingEntry `yaml:"staging"`
	Entries     []string       `yaml:"entries"`
	ImportRoots []string       `yaml:"import_roots,omitempty"`
}

type OpenAPI3Config struct {
	Files []string `yaml:"files"`
}

type GraphQLConfig struct {
	Schema    string                  `yaml:"schema"`
	Expose    *GraphQLExpose          `yaml:"expose,omitempty"`
	Groups    []GraphQLGroupPolicy    `yaml:"groups,omitempty"`
	Output    []GraphQLOutputPolicy   `yaml:"output,omitempty"`
	Selection *GraphQLSelectionPolicy `yaml:"selection,omitempty"`
}

type GraphQLExpose struct {
	Queries   []string `yaml:"queries,omitempty"`
	Mutations []string `yaml:"mutations,omitempty"`
}

type GraphQLGroupPolicy struct {
	Match []string `yaml:"match"`
	Group string   `yaml:"group"`
}

type GraphQLOutputPolicy struct {
	Match          []string `yaml:"match"`
	ListPath       string   `yaml:"list_path,omitempty"`
	DefaultColumns []string `yaml:"default_columns,omitempty"`
}

type GraphQLSelectionPolicy struct {
	MaxDepth *int     `yaml:"max_depth,omitempty"`
	Prune    []string `yaml:"prune,omitempty"`
}

type StagingEntry struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Sources) == 0 {
		return nil, fmt.Errorf("%s declares no sources", path)
	}
	baseDir := filepath.Dir(path)
	for name, src := range cfg.Sources {
		src.Name = name
		if err := validate(src, baseDir); err != nil {
			return nil, fmt.Errorf("source %q: %w", name, err)
		}
	}
	return &cfg, nil
}

func (c *Config) Ordered() []*Source {
	names := make([]string, 0, len(c.Sources))
	for n := range c.Sources {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*Source, 0, len(names))
	for _, n := range names {
		out = append(out, c.Sources[n])
	}
	return out
}

func validate(s *Source, baseDir string) error {
	if s.DefaultHostname != nil {
		hostname := latheconfig.NormalizeHostname(*s.DefaultHostname)
		if hostname == "" {
			return fmt.Errorf("default_hostname must not be empty")
		}
		*s.DefaultHostname = hostname
	}
	if s.LocalPath != "" {
		if s.RepoURL != "" {
			return fmt.Errorf("local_path cannot be used with repo_url")
		}
		if s.PinnedTag != "" {
			return fmt.Errorf("local_path cannot be used with pinned_tag")
		}
		localPath, err := resolveLocalPath(baseDir, s.LocalPath)
		if err != nil {
			return err
		}
		s.LocalPath = localPath
	} else {
		if s.RepoURL == "" {
			return fmt.Errorf("missing repo_url")
		}
		if s.PinnedTag == "" {
			return fmt.Errorf("missing pinned_tag")
		}
		if err := validateRef(s.PinnedTag); err != nil {
			return err
		}
	}
	switch s.Backend {
	case BackendSwagger:
		if s.Swagger == nil || len(s.Swagger.Files) == 0 {
			return fmt.Errorf("backend=swagger requires non-empty swagger.files")
		}
	case BackendProto:
		if s.Proto == nil || len(s.Proto.Entries) == 0 {
			return fmt.Errorf("backend=proto requires non-empty proto.entries")
		}
		if len(s.Proto.Staging) == 0 {
			return fmt.Errorf("backend=proto requires non-empty proto.staging")
		}
	case BackendOpenAPI3:
		if s.OpenAPI3 == nil || len(s.OpenAPI3.Files) == 0 {
			return fmt.Errorf("backend=openapi3 requires non-empty openapi3.files")
		}
	case BackendGraphQL:
		if s.GraphQL == nil || s.GraphQL.Schema == "" {
			return fmt.Errorf("backend=graphql requires graphql.schema")
		}
		if s.GraphQL.Expose == nil || (len(s.GraphQL.Expose.Queries) == 0 && len(s.GraphQL.Expose.Mutations) == 0) {
			return fmt.Errorf("backend=graphql requires an explicit graphql.expose policy (queries and/or mutations); refusing to expose the whole schema")
		}
		if err := validateGraphQLPolicy(s.GraphQL); err != nil {
			return err
		}
	case "":
		return fmt.Errorf("missing backend")
	default:
		return fmt.Errorf("unknown backend %q", s.Backend)
	}
	return rejectForeignBlocks(s)
}

func resolveLocalPath(baseDir, raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("local_path must not be empty")
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		if u.Scheme != "file" {
			return "", fmt.Errorf("local_path must be a filesystem path or file:// URL")
		}
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("local_path file:// URL must not include a remote host")
		}
		raw = filepath.FromSlash(u.Path)
		if raw == "" {
			return "", fmt.Errorf("local_path file:// URL must include a path")
		}
	} else if strings.Contains(raw, "://") {
		return "", fmt.Errorf("local_path must be a filesystem path or file:// URL")
	}
	if colon := strings.IndexByte(raw, ':'); colon > 0 && strings.Contains(raw[:colon], "@") && !strings.ContainsAny(raw[:colon], `/\`) {
		return "", fmt.Errorf("local_path must be a filesystem path or file:// URL")
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(baseDir, raw)
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve local_path: %w", err)
	}
	return abs, nil
}

func rejectForeignBlocks(s *Source) error {
	blocks := []struct {
		backend string
		set     bool
	}{
		{BackendSwagger, s.Swagger != nil},
		{BackendProto, s.Proto != nil},
		{BackendOpenAPI3, s.OpenAPI3 != nil},
		{BackendGraphQL, s.GraphQL != nil},
	}
	for _, b := range blocks {
		if b.backend != s.Backend && b.set {
			return fmt.Errorf("backend=%s must not set %s block", s.Backend, b.backend)
		}
	}
	return nil
}

func validateGraphQLPolicy(g *GraphQLConfig) error {
	if err := validateGraphQLPatterns("graphql.expose.queries", g.Expose.Queries); err != nil {
		return err
	}
	if err := validateGraphQLPatterns("graphql.expose.mutations", g.Expose.Mutations); err != nil {
		return err
	}
	for i, rule := range g.Groups {
		if strings.TrimSpace(rule.Group) == "" {
			return fmt.Errorf("graphql.groups[%d] requires group", i)
		}
		if len(rule.Match) == 0 {
			return fmt.Errorf("graphql.groups[%d] requires non-empty match", i)
		}
		if err := validateGraphQLPatterns(fmt.Sprintf("graphql.groups[%d].match", i), rule.Match); err != nil {
			return err
		}
	}
	for i, rule := range g.Output {
		if len(rule.Match) == 0 {
			return fmt.Errorf("graphql.output[%d] requires non-empty match", i)
		}
		if rule.ListPath == "" && len(rule.DefaultColumns) == 0 {
			return fmt.Errorf("graphql.output[%d] requires list_path or default_columns", i)
		}
		if err := validateGraphQLDottedPath(fmt.Sprintf("graphql.output[%d].list_path", i), rule.ListPath); err != nil {
			return err
		}
		for j, column := range rule.DefaultColumns {
			if strings.TrimSpace(column) == "" {
				return fmt.Errorf("graphql.output[%d].default_columns[%d] contains an empty path segment", i, j)
			}
			if err := validateGraphQLDottedPath(fmt.Sprintf("graphql.output[%d].default_columns[%d]", i, j), column); err != nil {
				return err
			}
		}
		if err := validateGraphQLPatterns(fmt.Sprintf("graphql.output[%d].match", i), rule.Match); err != nil {
			return err
		}
	}
	if g.Selection != nil {
		if g.Selection.MaxDepth != nil && *g.Selection.MaxDepth <= 0 {
			return fmt.Errorf("graphql.selection.max_depth must be > 0")
		}
		for _, p := range g.Selection.Prune {
			if _, err := path.Match(p, "Type.field"); err != nil {
				return fmt.Errorf("invalid graphql.selection.prune pattern %q: %w", p, err)
			}
			if !strings.Contains(p, ".") {
				return fmt.Errorf("graphql.selection.prune pattern %q must be Type.field", p)
			}
		}
	}
	return nil
}

func validateGraphQLPatterns(label string, patterns []string) error {
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("%s contains an empty pattern", label)
		}
		if _, err := path.Match(p, "sample"); err != nil {
			return fmt.Errorf("invalid %s pattern %q: %w", label, p, err)
		}
	}
	return nil
}

func validateGraphQLDottedPath(label string, p string) error {
	if p == "" {
		return nil
	}
	for _, part := range strings.Split(p, ".") {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%s contains an empty path segment", label)
		}
	}
	return nil
}

// validateRef rejects pinned_tag values that are obviously floating refs.
// The repo's README promises that every upstream spec is pinned at an
// immutable tag; accepting "HEAD" / "main" / "refs/heads/*" would silently
// break that promise. A 40-char hex string is treated as an explicit SHA.
// Anything else is checked for a small set of Git-illegal characters so
// typos fail fast here rather than during checkout.
func validateRef(ref string) error {
	if isFloating40Hex := len(ref) == 40 && allHex(ref); isFloating40Hex {
		return nil
	}
	switch ref {
	case "HEAD", "main", "master":
		return floatingRefError(ref)
	}
	if strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/remotes/") {
		return floatingRefError(ref)
	}
	if strings.HasPrefix(ref, "-") {
		return floatingRefError(ref)
	}
	if strings.ContainsAny(ref, " \t\r\n") {
		return floatingRefError(ref)
	}
	if strings.Contains(ref, "..") {
		return floatingRefError(ref)
	}
	for _, bad := range []string{"~", "^", ":", "?", "*", "[", "\\"} {
		if strings.Contains(ref, bad) {
			return floatingRefError(ref)
		}
	}
	return nil
}

func floatingRefError(ref string) error {
	return fmt.Errorf("pinned_tag %q looks like a floating ref; only immutable tags or 40-char SHAs are accepted", ref)
}

func allHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
