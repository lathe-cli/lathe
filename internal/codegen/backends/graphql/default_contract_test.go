package graphql

import (
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

func TestNonNullArgumentDefaultsRemainOptional(t *testing.T) {
	const sdl = `enum Mode { FAST SLOW } input Filter { count: Int! } type Query { items(limit: Int! = 10, mode: Mode! = FAST, ids: [ID!]! = [], filter: Filter! = {count: 1}, required: ID!): String! }`
	mod, err := parseSDL(t, sdl, []string{"items"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range mod.Operations[0].Parameters {
		if p.Required != (p.Name == "required") {
			t.Errorf("%s required = %t", p.Name, p.Required)
		}
	}
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: sdl})
	query := gqlparser.MustLoadQueryWithRules(schema, queryDocument(t, mod.Operations[0]), rules.NewDefaultRules())
	values, err := validator.VariableValues(schema, query.Operations[0], map[string]any{"required": "id"})
	if err != nil {
		t.Fatal(err)
	}
	if values["limit"] != int64(10) || values["mode"] != "FAST" || values["filter"].(map[string]any)["count"] != int64(1) {
		t.Fatalf("defaults = %#v", values)
	}
}
