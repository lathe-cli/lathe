package graphql

import (
	"fmt"
	"maps"
	"path"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

const defaultMaxSelectionDepth = 3

func (g *generator) selectionSet(typeName string, depth int, onPath map[string]bool) (string, error) {
	def := g.schema.Types[typeName]
	if def == nil {
		return "", fmt.Errorf("unknown type %q", typeName)
	}
	if def.IsLeafType() {
		return "", nil
	}
	if def.Kind == ast.Union {
		return "{ __typename }", nil
	}
	var fields []string
	for _, f := range def.Fields {
		pruned, err := g.pruned(def.Name, f.Name)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(f.Name, "__") || hasRequiredArgs(f) || pruned {
			continue
		}
		childName := f.Type.Name()
		child := g.schema.Types[childName]
		if child == nil {
			continue
		}
		if child.IsLeafType() {
			fields = append(fields, f.Name)
			continue
		}
		if depth >= g.selectionMaxDepth() || onPath[childName] {
			continue
		}
		next := maps.Clone(onPath)
		next[typeName] = true
		sub, err := g.selectionSet(childName, depth+1, next)
		if err != nil || sub == "" {
			continue
		}
		fields = append(fields, f.Name+" "+sub)
	}
	if len(fields) == 0 {
		return "", fmt.Errorf("type %q has no selectable fields within depth %d", typeName, g.selectionMaxDepth())
	}
	return "{ " + strings.Join(fields, " ") + " }", nil
}

func (g *generator) selectionMaxDepth() int {
	if g.config.Selection != nil && g.config.Selection.MaxDepth != nil {
		return *g.config.Selection.MaxDepth
	}
	return defaultMaxSelectionDepth
}

func (g *generator) pruned(typeName string, fieldName string) (bool, error) {
	if g.config.Selection == nil {
		return false, nil
	}
	target := typeName + "." + fieldName
	for _, pattern := range g.config.Selection.Prune {
		matched, err := path.Match(pattern, target)
		if err != nil {
			return false, fmt.Errorf("invalid graphql.selection.prune pattern %q: %w", pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func hasRequiredArgs(f *ast.FieldDefinition) bool {
	for _, a := range f.Arguments {
		if a.Type.NonNull && a.DefaultValue == nil {
			return true
		}
	}
	return false
}
