package sourceconfig

import (
	"fmt"
	"path"
	"strings"
)

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
