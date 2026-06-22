package sourceconfig

import (
	"fmt"
	"os"
	"sort"
	"strings"

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
	Name        string          `yaml:"-"`
	DisplayName string          `yaml:"display_name,omitempty"`
	RepoURL     string          `yaml:"repo_url"`
	PinnedTag   string          `yaml:"pinned_tag"`
	Backend     string          `yaml:"backend"`
	Swagger     *SwaggerConfig  `yaml:"swagger,omitempty"`
	Proto       *ProtoConfig    `yaml:"proto,omitempty"`
	OpenAPI3    *OpenAPI3Config `yaml:"openapi3,omitempty"`
	GraphQL     *GraphQLConfig  `yaml:"graphql,omitempty"`
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
	Schema string         `yaml:"schema"`
	Expose *GraphQLExpose `yaml:"expose,omitempty"`
}

type GraphQLExpose struct {
	Queries   []string `yaml:"queries,omitempty"`
	Mutations []string `yaml:"mutations,omitempty"`
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
	for name, src := range cfg.Sources {
		src.Name = name
		if err := validate(src); err != nil {
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

func validate(s *Source) error {
	if s.RepoURL == "" {
		return fmt.Errorf("missing repo_url")
	}
	if s.PinnedTag == "" {
		return fmt.Errorf("missing pinned_tag")
	}
	if err := validateRef(s.PinnedTag); err != nil {
		return err
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
	case "":
		return fmt.Errorf("missing backend")
	default:
		return fmt.Errorf("unknown backend %q", s.Backend)
	}
	return rejectForeignBlocks(s)
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
