package lathecmd

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/app"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestBuildWorkflowSpecs_CarriesConditions(t *testing.T) {
	specs, err := buildWorkflowSpecs(workflowManifestWithSteps([]config.WorkflowStep{{
		ID:   "deploy",
		Uses: "console.Apps_Get",
		When: []config.WorkflowCondition{{
			Value:    "${input.kind}",
			Operator: "in",
			Values:   config.WorkflowConditionValues{"gpu"},
		}},
	}}), conditionTestModules(), nil)
	if err != nil {
		t.Fatalf("buildWorkflowSpecs: %v", err)
	}
	got := specs[0].Steps[0].When
	if len(got) != 1 || got[0].Value != "${input.kind}" || got[0].Operator != "in" {
		t.Fatalf("conditions = %#v", got)
	}
	if len(got[0].Values) != 1 || got[0].Values[0] != "gpu" {
		t.Fatalf("values = %#v", got[0].Values)
	}
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
					Values:   config.WorkflowConditionValues{"gpu"},
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
					Values:   config.WorkflowConditionValues{"gpu"},
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
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
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
