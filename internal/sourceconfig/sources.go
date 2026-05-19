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
		if s.Proto != nil {
			return fmt.Errorf("backend=swagger must not set proto block")
		}
		if s.OpenAPI3 != nil {
			return fmt.Errorf("backend=swagger must not set openapi3 block")
		}
	case BackendProto:
		if s.Proto == nil || len(s.Proto.Entries) == 0 {
			return fmt.Errorf("backend=proto requires non-empty proto.entries")
		}
		if len(s.Proto.Staging) == 0 {
			return fmt.Errorf("backend=proto requires non-empty proto.staging")
		}
		if s.Swagger != nil {
			return fmt.Errorf("backend=proto must not set swagger block")
		}
		if s.OpenAPI3 != nil {
			return fmt.Errorf("backend=proto must not set openapi3 block")
		}
	case BackendOpenAPI3:
		if s.OpenAPI3 == nil || len(s.OpenAPI3.Files) == 0 {
			return fmt.Errorf("backend=openapi3 requires non-empty openapi3.files")
		}
		if s.Swagger != nil {
			return fmt.Errorf("backend=openapi3 must not set swagger block")
		}
		if s.Proto != nil {
			return fmt.Errorf("backend=openapi3 must not set proto block")
		}
	case "":
		return fmt.Errorf("missing backend")
	default:
		return fmt.Errorf("unknown backend %q", s.Backend)
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
