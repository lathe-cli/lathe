package graphql

import (
	"fmt"
	"path"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/vektah/gqlparser/v2/ast"
)

func (g *generator) groupFor(fieldName string) (string, error) {
	var group string
	for _, rule := range g.config.Groups {
		matched, err := matchAny(rule.Match, fieldName)
		if err != nil {
			return "", err
		}
		if matched {
			if group != "" {
				return "", fmt.Errorf("operation %q matches multiple graphql.groups rules", fieldName)
			}
			group = rule.Group
		}
	}
	if group != "" {
		return group, nil
	}
	return g.module, nil
}

func (g *generator) outputFor(field *ast.FieldDefinition) (*rawir.RawOutputHints, error) {
	fieldName := field.Name
	var output *rawir.RawOutputHints
	for _, rule := range g.config.Output {
		matched, err := matchAny(rule.Match, fieldName)
		if err != nil {
			return nil, err
		}
		if matched {
			if output != nil {
				return nil, fmt.Errorf("operation %q matches multiple graphql.output rules", fieldName)
			}
			output = &rawir.RawOutputHints{
				ListPath:       rule.ListPath,
				DefaultColumns: append([]string(nil), rule.DefaultColumns...),
			}
		}
	}
	if output != nil {
		pagination, err := g.relayPaginationFor(field, output.ListPath)
		if err != nil {
			return nil, err
		}
		output.Pagination = pagination
	}
	return output, nil
}

func (g *generator) relayPaginationFor(field *ast.FieldDefinition, listPath string) (*rawir.RawPaginationHint, error) {
	if listPath == "" {
		return nil, nil
	}
	wantPrefix := "data." + field.Name + "."
	if listPath != wantPrefix+"nodes" && listPath != wantPrefix+"edges" {
		return nil, nil
	}
	if field.Arguments.ForName("after") == nil {
		return nil, nil
	}
	if g.selectionMaxDepth() <= 1 {
		return nil, nil
	}
	connection := g.schema.Types[field.Type.Name()]
	if connection == nil {
		return nil, nil
	}
	pruned, err := g.pruned(connection.Name, "pageInfo")
	if err != nil || pruned {
		return nil, err
	}
	pageInfoField := connection.Fields.ForName("pageInfo")
	if pageInfoField == nil {
		return nil, nil
	}
	pageInfo := g.schema.Types[pageInfoField.Type.Name()]
	if pageInfo == nil {
		return nil, nil
	}
	for _, fieldName := range []string{"endCursor", "hasNextPage"} {
		pruned, err := g.pruned(pageInfo.Name, fieldName)
		if err != nil || pruned || pageInfo.Fields.ForName(fieldName) == nil {
			return nil, err
		}
	}
	limitParam := ""
	if field.Arguments.ForName("first") != nil {
		limitParam = "variables.first"
	}
	return &rawir.RawPaginationHint{
		Strategy:   "body-cursor",
		TokenParam: "variables.after",
		TokenField: wantPrefix + "pageInfo.endCursor",
		LimitParam: limitParam,
	}, nil
}

func matchAny(patterns []string, name string) (bool, error) {
	for _, p := range patterns {
		ok, err := path.Match(p, name)
		if err != nil {
			return false, fmt.Errorf("invalid expose pattern %q: %w", p, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
