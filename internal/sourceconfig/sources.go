package sourceconfig

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	latheconfig "github.com/lathe-cli/lathe/pkg/config"
	"gopkg.in/yaml.v3"
)

const (
	BackendSwagger  = "swagger"
	BackendProto    = "proto"
	BackendOpenAPI3 = "openapi3"
	BackendGraphQL  = "graphql"

	ProtoDependencyBuf      = "buf"
	ProtoDependencyGoModule = "go_module"
	ProtoDependencyGit      = "git"
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
	Staging      []StagingEntry    `yaml:"staging"`
	Entries      []string          `yaml:"entries"`
	ImportRoots  []string          `yaml:"import_roots,omitempty"`
	Dependencies []ProtoDependency `yaml:"dependencies,omitempty"`
}

type ProtoDependency struct {
	Kind      string         `yaml:"kind"`
	Module    string         `yaml:"module,omitempty"`
	Version   string         `yaml:"version,omitempty"`
	Sum       string         `yaml:"sum,omitempty"`
	Commit    string         `yaml:"commit,omitempty"`
	Digest    string         `yaml:"digest,omitempty"`
	RepoURL   string         `yaml:"repo_url,omitempty"`
	PinnedTag string         `yaml:"pinned_tag,omitempty"`
	Staging   []StagingEntry `yaml:"staging"`
}

type OpenAPI3Config struct {
	Files  []string       `yaml:"files"`
	Expose *OpenAPIExpose `yaml:"expose,omitempty"`
}

type OpenAPIExpose struct {
	OperationIDs []string `yaml:"operation_ids"`
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
		if err := validateSourceID(name); err != nil {
			return nil, err
		}
		src.Name = name
		if err := validate(src, baseDir); err != nil {
			return nil, fmt.Errorf("source %q: %w", name, err)
		}
	}
	return &cfg, nil
}

func (c *Config) Ordered() []*Source {
	names := slices.Sorted(maps.Keys(c.Sources))
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
		if err := validateRelPathList("swagger.files", s.Swagger.Files); err != nil {
			return err
		}
	case BackendProto:
		if s.Proto == nil || len(s.Proto.Entries) == 0 {
			return fmt.Errorf("backend=proto requires non-empty proto.entries")
		}
		if len(s.Proto.Staging) == 0 {
			return fmt.Errorf("backend=proto requires non-empty proto.staging")
		}
		if err := validateStaging("proto.staging", s.Proto.Staging); err != nil {
			return err
		}
		if err := validateRelPathList("proto.entries", s.Proto.Entries); err != nil {
			return err
		}
		for _, entry := range s.Proto.Entries {
			if strings.HasPrefix(entry, "-") || strings.HasPrefix(entry, "@") {
				return fmt.Errorf("proto.entries contains protoc control argument %q", entry)
			}
		}
		if err := validateRelPathList("proto.import_roots", s.Proto.ImportRoots); err != nil {
			return err
		}
		if err := validateProtoDependencies(s.Proto.Dependencies); err != nil {
			return err
		}
	case BackendOpenAPI3:
		if s.OpenAPI3 == nil || len(s.OpenAPI3.Files) == 0 {
			return fmt.Errorf("backend=openapi3 requires non-empty openapi3.files")
		}
		if err := validateRelPathList("openapi3.files", s.OpenAPI3.Files); err != nil {
			return err
		}
		if s.OpenAPI3.Expose != nil {
			if len(s.OpenAPI3.Expose.OperationIDs) == 0 {
				return fmt.Errorf("openapi3.expose requires non-empty operation_ids")
			}
			seen := map[string]bool{}
			for _, operationID := range s.OpenAPI3.Expose.OperationIDs {
				if strings.TrimSpace(operationID) == "" {
					return fmt.Errorf("openapi3.expose.operation_ids must not contain empty values")
				}
				if seen[operationID] {
					return fmt.Errorf("openapi3.expose.operation_ids contains duplicate %q", operationID)
				}
				seen[operationID] = true
			}
		}
	case BackendGraphQL:
		if s.GraphQL == nil || s.GraphQL.Schema == "" {
			return fmt.Errorf("backend=graphql requires graphql.schema")
		}
		if err := ValidateRelPath("graphql.schema", s.GraphQL.Schema); err != nil {
			return err
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

func validateProtoDependencies(deps []ProtoDependency) error {
	for i, dep := range deps {
		field := fmt.Sprintf("proto.dependencies[%d]", i)
		if len(dep.Staging) == 0 {
			return fmt.Errorf("%s requires non-empty staging", field)
		}
		if err := validateStaging(field+".staging", dep.Staging); err != nil {
			return err
		}
		switch dep.Kind {
		case ProtoDependencyBuf:
			if dep.Module == "" || len(dep.Commit) != 32 || !allHex(dep.Commit) || len(dep.Digest) <= 3 || !strings.HasPrefix(dep.Digest, "b5:") || !allHex(strings.TrimPrefix(dep.Digest, "b5:")) {
				return fmt.Errorf("%s kind=buf requires module, 32-character commit, and a b5 digest", field)
			}
			if dep.Version != "" || dep.Sum != "" || dep.RepoURL != "" || dep.PinnedTag != "" {
				return fmt.Errorf("%s kind=buf contains fields for another dependency kind", field)
			}
		case ProtoDependencyGoModule:
			if dep.Module == "" || dep.Version == "" || len(dep.Sum) <= 3 || !strings.HasPrefix(dep.Sum, "h1:") {
				return fmt.Errorf("%s kind=go_module requires module, version, and h1 sum", field)
			}
			if err := validateRef(dep.Version); err != nil {
				return fmt.Errorf("%s.version: %w", field, err)
			}
			if dep.Commit != "" || dep.Digest != "" || dep.RepoURL != "" || dep.PinnedTag != "" {
				return fmt.Errorf("%s kind=go_module contains fields for another dependency kind", field)
			}
		case ProtoDependencyGit:
			if dep.RepoURL == "" || dep.PinnedTag == "" {
				return fmt.Errorf("%s kind=git requires repo_url and pinned_tag", field)
			}
			if err := validateRef(dep.PinnedTag); err != nil {
				return fmt.Errorf("%s.pinned_tag: %w", field, err)
			}
			if dep.Module != "" || dep.Version != "" || dep.Sum != "" || dep.Commit != "" || dep.Digest != "" {
				return fmt.Errorf("%s kind=git contains fields for another dependency kind", field)
			}
		default:
			return fmt.Errorf("%s has unknown kind %q", field, dep.Kind)
		}
		if dep.Module != "" {
			if strings.ContainsAny(dep.Module, " \t\r\n:") {
				return fmt.Errorf("%s.module is invalid", field)
			}
			if err := ValidateRelPath(field+".module", dep.Module); err != nil {
				return err
			}
		}
	}
	return nil
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
