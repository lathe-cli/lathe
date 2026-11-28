package render

import (
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"testing"
)

func TestOverlayRejectsUnmatchedAndAmbiguousInputs(t *testing.T) {
	specs := []runtime.CommandSpec{{Group: "items", Use: "get", Params: []runtime.ParamSpec{{Name: "id", Flag: "query-id", In: runtime.InQuery, GoType: "string"}, {Name: "id", Flag: "header-id", In: runtime.InHeader, GoType: "string"}}}, {Group: "items", Use: "list"}}
	for name, commands := range map[string]map[string]overlay.Override{
		"unknown command":     {"typo": {}},
		"unmatched selector":  {"get": {Match: overlay.OperationMatch{Method: "DELETE"}}},
		"unknown parameter":   {"get": {Params: map[string]overlay.ParamOverride{"typo": {Help: "help"}}}},
		"ambiguous parameter": {"get": {Params: map[string]overlay.ParamOverride{"id": {Help: "help"}}}},
		"duplicate use":       {"get": {Use: "list"}},
		"alias collision":     {"get": {Aliases: []string{"list"}}},
	} {
		t.Run(name, func(t *testing.T) {
			mod := overlay.Module{Commands: commands}
			if err := ValidateOverlayModule(specs, mod); err == nil {
				t.Fatal("invalid overlay accepted")
			}
			if _, err := MergeOverlayModule(specs, mod); err == nil {
				t.Fatal("invalid merge accepted")
			}
		})
	}
}

func TestOverlayExpandsBodyBeforeArgumentValidation(t *testing.T) {
	specs := []runtime.CommandSpec{{Use: "create", RequestBody: &runtime.RequestBody{MediaType: "application/json", Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}}}}}}
	mod := overlay.Module{Commands: map[string]overlay.Override{"create": {Body: &overlay.BodyOverride{Flags: true}, Params: map[string]overlay.ParamOverride{"body:name": {Argument: "name"}}}}}
	if err := ValidateOverlayModule(specs, mod); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeOverlayModule(specs, mod)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged[0].Params) != 1 || merged[0].Params[0].Argument != "name" {
		t.Fatalf("params = %+v", merged[0].Params)
	}
}
