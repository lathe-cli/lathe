package render

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestMergeOverlayModule_BodyFlags(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group:  "keys",
		Use:    "update-limits",
		Method: "PATCH",
		Params: []runtime.ParamSpec{{Name: "id", Flag: "id", In: runtime.InPath, GoType: "string", Required: true}},
		RequestBody: &runtime.RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &runtime.SchemaSpec{
				Type: "object",
				Properties: map[string]*runtime.SchemaSpec{
					"maxBudgetUsd":   {Type: "number", Nullable: true},
					"budgetDuration": {Type: "string", Nullable: true, Enum: []string{"monthly"}},
					"limits":         {Type: "object", Properties: map[string]*runtime.SchemaSpec{"rpm": {Type: "integer"}}},
				},
			},
		},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"update-limits": {
			Use:  "replace-limits",
			Body: &overlay.BodyOverride{Flags: true},
			Params: map[string]overlay.ParamOverride{
				"maxBudgetUsd": {Help: "Budget in USD"},
			},
		},
	}}
	testutil.NoError(t, ValidateOverlayModule(specs, mod))
	merged := mustMergeOverlayModule(t, specs, mod)
	testutil.Require(t, merged[0].Use == "replace-limits", "use = %q", merged[0].Use)
	testutil.Require(t, len(merged[0].Params) == 3, "params = %#v", merged[0].Params)
	byName := map[string]runtime.ParamSpec{}
	for _, param := range merged[0].Params {
		byName[param.Name] = param
	}
	testutil.Require(t, byName["maxBudgetUsd"].In == runtime.InBody && byName["maxBudgetUsd"].Flag == "max-budget-usd" && byName["maxBudgetUsd"].Help == "Budget in USD", "maxBudgetUsd = %+v", byName["maxBudgetUsd"])
	testutil.Require(t, byName["budgetDuration"].Enum[0] == "monthly", "budgetDuration = %+v", byName["budgetDuration"])
	if _, ok := byName["limits"]; ok {
		t.Fatalf("nested object property must not produce a typed flag: %#v", byName["limits"])
	}
	if got := merged[0].RequestBody.SetOnlyFields; len(got) != 1 || got[0] != "limits" {
		t.Fatalf("set-only fields = %#v", got)
	}
}

func TestRenderModule_EmitsSetOnlyBodyFields(t *testing.T) {
	chdirWithGoMod(t)

	specs := []runtime.CommandSpec{{
		Group:   "Keys",
		Use:     "create-key",
		Short:   "Create a key",
		Method:  "POST",
		PathTpl: "/keys",
		RequestBody: &runtime.RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &runtime.SchemaSpec{
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]*runtime.SchemaSpec{
					"name":   {Type: "string"},
					"limits": {Type: "object", Properties: map[string]*runtime.SchemaSpec{"maxBudgetUsd": {Type: "number"}}},
				},
			},
		},
	}}
	overrides := map[string]overlay.Override{"create-key": {Body: &overlay.BodyOverride{Flags: true}}}
	testutil.NoError(t, RenderModule("demo", "", specs, overrides))
	flat := generatedModule(t, "demo")
	testutil.Check(t, containsGo(flat, `SetOnlyFields: []string{"limits"}`), "output missing set-only fields literal:\n%s", flat)
	testutil.Check(t, containsGo(flat, `Flag: "name"`), "output missing typed flag for top-level scalar:\n%s", flat)
}

func TestValidateOverlayModule_RejectsNestedBodyFlags(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "keys",
		Use:   "create",
		RequestBody: &runtime.RequestBody{
			MediaType: "application/json",
			Schema: &runtime.SchemaSpec{
				Type: "object",
				Properties: map[string]*runtime.SchemaSpec{
					"limits": {Type: "object", Properties: map[string]*runtime.SchemaSpec{"rpm": {Type: "integer"}}},
				},
			},
		},
	}}
	err := ValidateOverlayModule(specs, overlay.Module{Commands: map[string]overlay.Override{
		"create": {Body: &overlay.BodyOverride{Flags: true}},
	}})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "no body properties support typed flags"), "error = %v", err)
}

func TestValidateOverlayModule_RejectsBodyFlagOverrideCollision(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group:  "users",
		Use:    "update",
		Method: "PATCH",
		Params: []runtime.ParamSpec{{Name: "id", Flag: "id", In: runtime.InPath, GoType: "string", Required: true}},
		RequestBody: &runtime.RequestBody{MediaType: "application/json", Schema: &runtime.SchemaSpec{
			Type:       "object",
			Properties: map[string]*runtime.SchemaSpec{"name": {Type: "string"}},
		}},
	}}
	err := ValidateOverlayModule(specs, overlay.Module{Commands: map[string]overlay.Override{
		"update": {
			Body:   &overlay.BodyOverride{Flags: true},
			Params: map[string]overlay.ParamOverride{"name": {Flag: "id"}},
		},
	}})
	testutil.Require(t, err != nil, "expected duplicate flag validation error")
	specs[0].Params = []runtime.ParamSpec{{Name: "tokenEnv", Flag: "token-env", In: runtime.InQuery, GoType: "string"}}
	specs[0].RequestBody.Schema.Properties = map[string]*runtime.SchemaSpec{"token": {Type: "string"}}
	err = ValidateOverlayModule(specs, overlay.Module{Commands: map[string]overlay.Override{
		"update": {Body: &overlay.BodyOverride{Flags: true}},
	}})
	testutil.Require(t, err != nil, "expected sensitive input companion collision error")
}

func TestMergeOverlayModule_ReturnsBodyFlagExpansionError(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "users",
		Use:   "update",
		RequestBody: &runtime.RequestBody{MediaType: "application/json", Schema: &runtime.SchemaSpec{
			Type:       "object",
			Properties: map[string]*runtime.SchemaSpec{"profile": {Type: "object"}},
		}},
	}}
	_, err := MergeOverlayModule(specs, overlay.Module{Commands: map[string]overlay.Override{
		"update": {Body: &overlay.BodyOverride{Flags: true}},
	}})
	testutil.Require(t, err != nil, "expected body flag expansion error")
}
