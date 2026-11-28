package app

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestValidateContexts(t *testing.T) {
	manifest := &config.Manifest{Contexts: map[string]config.ContextInfo{"workspace": {}}}
	valid := runtime.CommandSpec{Use: "use", Params: []runtime.ParamSpec{{Name: "workspace_id", Flag: "workspace-id", GoType: "string", Context: "workspace"}}, SetContext: &runtime.ContextSetHint{Name: "workspace", Param: "workspace_id"}}
	testutil.NoError(t, (&App{Manifest: manifest, Modules: []Module{{CLIName: "api", Specs: []runtime.CommandSpec{valid}}}}).Validate())

	invalid := valid
	invalid.Params = append([]runtime.ParamSpec(nil), valid.Params...)
	invalid.Params[0].Context = "unknown"
	if err := (&App{Manifest: manifest, Modules: []Module{{CLIName: "api", Specs: []runtime.CommandSpec{invalid}}}}).Validate(); err == nil {
		t.Fatal("unknown context accepted")
	}
}

func TestValidateMultipartBody_AllOfRequiredUnsupported(t *testing.T) {
	spec := runtime.CommandSpec{
		Use: "create",
		RequestBody: &runtime.RequestBody{
			MediaType:         "multipart/form-data",
			UnsupportedFields: []string{"extra"},
			Schema:            &runtime.SchemaSpec{AllOf: []*runtime.SchemaSpec{{Required: []string{"extra"}}}},
		},
	}
	err := (&App{Modules: []Module{{CLIName: "api", Specs: []runtime.CommandSpec{spec}}}}).Validate()
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "extra"), "error = %v", err)
}

func TestValidateQualifiedContextSource(t *testing.T) {
	manifest := &config.Manifest{Contexts: map[string]config.ContextInfo{"workspace": {}}}
	spec := runtime.CommandSpec{Params: []runtime.ParamSpec{
		{Name: "id", Flag: "query-id", In: runtime.InQuery, GoType: "string"},
		{Name: "id", Flag: "header-id", In: runtime.InHeader, GoType: "string"},
	}, SetContext: &runtime.ContextSetHint{Name: "workspace", Param: "header:id"}}
	if err := validateCommandContexts(manifest, spec); err != nil {
		t.Fatal(err)
	}
	spec.SetContext.Param = "id"
	if err := validateCommandContexts(manifest, spec); err == nil {
		t.Fatal("ambiguous context source accepted")
	}
}
