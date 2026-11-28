package lathecmd

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/app"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuildWorkflowSpecs_CarriesConditions(t *testing.T) {
	specs, err := buildWorkflowSpecs(workflowManifestWithSteps([]config.WorkflowStep{{
		ID:   "deploy",
		Uses: "console.Apps_Get",
		When: []config.WorkflowCondition{{
			Value:    "${input.kind}",
			Operator: "in",
			Values:   []string{"gpu"},
		}},
	}}), conditionTestModules(), nil)
	testutil.Require(t, err == nil, "buildWorkflowSpecs: %v", err)
	got := specs[0].Steps[0].When
	testutil.Require(t, len(got) == 1 && got[0].Value == "${input.kind}" && got[0].Operator == "in", "conditions = %#v", got)
	testutil.Require(t, len(got[0].Values) == 1 && got[0].Values[0] == "gpu", "values = %#v", got[0].Values)
}

func TestBuildWorkflowSpecs_RejectsBinaryStep(t *testing.T) {
	modules := conditionTestModules()
	modules[0].Specs[0].Output.Binary = true
	_, err := buildWorkflowSpecs(workflowManifestWithSteps([]config.WorkflowStep{{
		ID:   "deploy",
		Uses: "console.Apps_Get",
	}}), modules, nil)
	want := `workflow command "deploy" step "deploy": operation "Apps_Get" returns a binary response; workflow steps cannot consume binary responses`
	testutil.Require(t, err != nil && err.Error() == want, "error = %v", err)
}

func TestBuildWorkflowSpecs_RejectsBadConditionReferences(t *testing.T) {
	cases := map[string]struct {
		steps []config.WorkflowStep
		want  string
	}{
		"unknown input": {
			steps: []config.WorkflowStep{{
				ID:   "deploy",
				Uses: "console.Apps_Get",
				When: []config.WorkflowCondition{{
					Value:    "${input.missing}",
					Operator: "in",
					Values:   []string{"gpu"},
				}},
			}},
			want: `unknown input "missing"`,
		},
		"forward step": {
			steps: []config.WorkflowStep{{
				ID:   "deploy",
				Uses: "console.Apps_Get",
				When: []config.WorkflowCondition{{
					Value:    "${steps.later.id}",
					Operator: "in",
					Values:   []string{"gpu"},
				}},
			}, {
				ID:   "later",
				Uses: "console.Apps_Get",
			}},
			want: `unknown or forward step "later"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := buildWorkflowSpecs(workflowManifestWithSteps(tc.steps), conditionTestModules(), nil)
			testutil.Require(t, err != nil && strings.Contains(err.Error(), tc.want), "error = %v, want it to contain %q", err, tc.want)
		})
	}
}

func workflowManifestWithSteps(steps []config.WorkflowStep) *config.Manifest {
	return &config.Manifest{
		Workflow: config.WorkflowInfo{
			Version: 1,
			Commands: []config.WorkflowCommand{{
				Use:    "deploy",
				Inputs: []config.WorkflowInput{{Name: "kind", Flag: "kind", Type: "string"}},
				Steps:  steps,
			}},
		},
	}
}

func conditionTestModules() []app.Module {
	return []app.Module{{
		Source:  "console",
		CLIName: "console",
		Specs: []runtime.CommandSpec{{
			OperationID: "Apps_Get",
			Group:       "Apps",
			Use:         "get",
			Method:      "GET",
			PathTpl:     "/apps",
		}},
	}}
}

func TestBuildWorkflowSpecsRejectsDerivedInputFlagCollision(t *testing.T) {
	manifest, err := config.Load([]byte(`cli:
  name: console
workflow:
  version: 1
  commands:
    - use: deploy
      inputs:
        - name: api_key
        - name: api-key-file
      steps:
        - id: get
          uses: console.Apps_Get
`))
	testutil.NoError(t, err)
	_, err = buildWorkflowSpecs(manifest, conditionTestModules(), nil)
	if err == nil {
		t.Fatal("expected derived input flag collision error")
	}
}

func TestWorkflowQualifiedParameterBindings(t *testing.T) {
	spec := runtime.CommandSpec{OperationID: "get", Params: []runtime.ParamSpec{
		{Name: "id", Flag: "query-id", In: runtime.InQuery, GoType: "string"},
		{Name: "id", Flag: "header-id", In: runtime.InHeader, GoType: "string"},
	}}
	for _, key := range []string{"query:id", "header:id", "query-id", "header-id"} {
		if err := validateWorkflowStepParams(config.WorkflowStep{Params: map[string]string{key: "value"}}, spec); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	if err := validateWorkflowStepParams(config.WorkflowStep{Params: map[string]string{"id": "value"}}, spec); err == nil {
		t.Fatal("ambiguous workflow parameter accepted")
	}
}
