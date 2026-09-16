package render

import (
	"testing"

	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestRenderWorkflows_EmitsPointerFieldLiterals(t *testing.T) {
	chdirWithGeneratedRoot(t)

	specs := []runtime.WorkflowSpec{{
		Use: "doctor",
		Steps: []runtime.WorkflowStepSpec{{
			ID: "create",
			Operation: runtime.CommandSpec{
				Group:       "Apps",
				Use:         "create-app",
				Short:       "Create an app.",
				Method:      "POST",
				PathTpl:     "/apps",
				OperationID: "Apps_Create",
				RequestBody: &runtime.RequestBody{
					Required:  true,
					MediaType: "application/json",
					Schema: &runtime.SchemaSpec{
						Type:     "object",
						Required: []string{"name"},
					},
				},
				Output: runtime.OutputHints{
					Pagination: &runtime.PaginationHint{
						Strategy:   "token",
						TokenParam: "page_token",
					},
				},
				Security: &runtime.SecurityHint{Scopes: []string{"apps:write"}},
			},
		}},
	}}

	testutil.NoError(t, RenderWorkflows(specs))
	got := generatedFile(t, "workflows/workflows_gen.go")
	for _, want := range []string{
		`RequestBody: &runtime.RequestBody{`,
		`Schema: &runtime.SchemaSpec{`,
		`Pagination: &runtime.PaginationHint{`,
		`Security: &runtime.SecurityHint{`,
	} {
		testutil.Check(t, containsGo(got, want), "output missing %q", want)
	}
	for _, bad := range []string{"(*runtime.RequestBody)", "(*runtime.SecurityHint)", "(*runtime.PaginationHint)", "(0x"} {
		testutil.Require(t, !containsGo(got, bad), "workflow literal contains pointer address %q:\n%s", bad, got)
	}
}

func TestRenderWorkflows_EmitsStepConditions(t *testing.T) {
	chdirWithGeneratedRoot(t)

	specs := []runtime.WorkflowSpec{{
		Use: "deploy",
		Steps: []runtime.WorkflowStepSpec{{
			ID: "gpu",
			Operation: runtime.CommandSpec{
				Group:       "Apps",
				Use:         "deploy-gpu",
				Method:      "POST",
				PathTpl:     "/apps/gpu",
				OperationID: "Apps_DeployGPU",
			},
			When: []runtime.WorkflowCondition{
				{Value: "${input.kind}", Operator: "in", Values: []string{"gpu", "404"}},
				{Value: "${input.label}", Operator: "notin", Values: []string{""}},
			},
		}},
	}}

	testutil.NoError(t, RenderWorkflows(specs))
	got := generatedFile(t, "workflows/workflows_gen.go")
	for _, want := range []string{
		`When: []runtime.WorkflowCondition{`,
		`Value: "${input.kind}"`,
		`Operator: "in"`,
		`Values: []string{"gpu", "404"}`,
		`Operator: "notin"`,
	} {
		testutil.Check(t, containsGo(got, want), "output missing %q:\n%s", want, got)
	}
}
