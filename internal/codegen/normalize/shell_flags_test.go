package normalize

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestParameterFlagsDoNotRequireShellQuoting(t *testing.T) {
	specs := Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{{Group: "Items", OperationID: "list", Method: "GET", Path: "/items", Parameters: []rawir.RawParameter{
		{Name: "filter[status]", In: "query"}, {Name: "filter-status", In: "query"}, {Name: "*?{}", In: "query"},
		{Name: "a[b]", In: "query"}, {Name: "a-b", In: "query"}, {Name: "a_b_2", In: "query"},
	}}}})
	seen := map[string]bool{}
	for _, p := range specs[0].Params {
		if p.Flag == "" || strings.ContainsAny(p.Flag, "[]*?{}") || seen[p.Flag] {
			t.Errorf("unsafe or colliding flag: %+v", p)
		}
		seen[p.Flag] = true
	}
}

func TestCrossLocationParametersKeepDistinctBindings(t *testing.T) {
	specs := Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{{Group: "Items", OperationID: "get", Method: "GET", Path: "/items/{id}", Parameters: []rawir.RawParameter{
		{Name: "id", In: "query"}, {Name: "id", In: "path"},
	}}}})
	if err := runtime.ValidateParamFlags(specs[0].Params); err != nil {
		t.Fatal(err)
	}
	if specs[0].Params[0].Flag != "id" || specs[0].Params[1].Flag != "path-id" {
		t.Fatalf("bindings = %+v", specs[0].Params)
	}
}
