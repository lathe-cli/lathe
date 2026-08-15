package app

import (
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestValidateContexts(t *testing.T) {
	manifest := &config.Manifest{Contexts: map[string]config.ContextInfo{"workspace": {}}}
	valid := runtime.CommandSpec{Use: "use", Params: []runtime.ParamSpec{{Name: "workspace_id", Flag: "workspace-id", GoType: "string", Context: "workspace"}}, SetContext: &runtime.ContextSetHint{Name: "workspace", Param: "workspace_id"}}
	if err := (&App{Manifest: manifest, Modules: []Module{{CLIName: "api", Specs: []runtime.CommandSpec{valid}}}}).Validate(); err != nil {
		t.Fatalf("valid context: %v", err)
	}

	invalid := valid
	invalid.Params = append([]runtime.ParamSpec(nil), valid.Params...)
	invalid.Params[0].Context = "unknown"
	if err := (&App{Manifest: manifest, Modules: []Module{{CLIName: "api", Specs: []runtime.CommandSpec{invalid}}}}).Validate(); err == nil {
		t.Fatal("unknown context accepted")
	}
}
