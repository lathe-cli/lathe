package graphql

import "testing"

func TestNonNullArgumentDefaultsRemainOptional(t *testing.T) {
	mod, err := parseSDL(t, `enum Mode { FAST SLOW } type Query { items(limit: Int! = 10, mode: Mode! = FAST, ids: [ID!]! = [], required: ID!): String! }`, []string{"items"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range mod.Operations[0].Parameters {
		if p.Required != (p.Name == "required") {
			t.Errorf("%s required = %t", p.Name, p.Required)
		}
	}
}
