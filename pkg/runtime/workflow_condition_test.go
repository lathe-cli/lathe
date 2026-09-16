package runtime

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuildWorkflows_WhenSelectsBranch(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := workflowServer(t, &paths, `{"ok":true}`)

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{
			{
				ID:        "gpu",
				Operation: publicGetSpec("gpu", "/gpu"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
			},
			{
				ID:        "cpu",
				Operation: publicGetSpec("cpu", "/cpu"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"cpu"}}},
			},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, reflect.DeepEqual(paths, []string{"/cpu"}), "paths = %#v, want only /cpu", paths)
	summary := decodeWorkflowSummary(t, stdout.String())
	testutil.Require(t, summary.Status == "ok", "status = %q", summary.Status)
	if got := stepStatuses(summary); !reflect.DeepEqual(got, map[string]string{"gpu": "skipped", "cpu": "ok"}) {
		t.Fatalf("step statuses = %#v", got)
	}
}

func TestBuildWorkflows_WhenTreatsUnsetInputAsEmpty(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	root := newWorkflowRoot(io.Discard)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "label", Flag: "label", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{{
			ID:        "label",
			Operation: publicGetSpec("label", "/label"),
			When:      []WorkflowCondition{{Value: "${input.label}", Operator: "notin", Values: []string{""}}},
		}},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, len(paths) == 0, "paths = %#v, want no request when --label is unset", paths)
}

func TestBuildWorkflows_SkipPropagatesThroughParamsAndConditions(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := workflowServer(t, &paths, `{"id":"abc"}`)

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{
			{
				ID:        "gpu",
				Operation: publicGetSpec("gpu", "/gpu"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
			},

			{
				ID: "notify",
				Operation: CommandSpec{
					Group:    "System",
					Use:      "notify",
					Method:   "GET",
					PathTpl:  "/notify/{id}",
					Params:   []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true}},
					Security: &SecurityHint{Public: true},
				},
				Params: map[string]string{"id": "${steps.gpu.id}"},
			},

			{
				ID:        "audit",
				Operation: publicGetSpec("audit", "/audit"),
				When:      []WorkflowCondition{{Value: "${steps.gpu.id}", Operator: "in", Values: []string{"abc"}}},
			},

			{
				ID:        "trail",
				Operation: publicGetSpec("trail", "/trail"),
				When:      []WorkflowCondition{{Value: "${steps.audit.ok}", Operator: "in", Values: []string{"true"}}},
			},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, len(paths) == 0, "paths = %#v, want every step skipped", paths)
	summary := decodeWorkflowSummary(t, stdout.String())
	testutil.Require(t, summary.Status == "ok", "status = %q, want ok", summary.Status)
	want := map[string]string{"gpu": "skipped", "notify": "skipped", "audit": "skipped", "trail": "skipped"}
	if got := stepStatuses(summary); !reflect.DeepEqual(got, want) {
		t.Fatalf("step statuses = %#v", got)
	}
}

func TestBuildWorkflows_SkippedStepDoesNotLoadAuth(t *testing.T) {
	isolateRuntime(t)

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{{
			ID: "guarded",
			Operation: CommandSpec{
				Group:   "System",
				Use:     "guarded",
				Method:  "GET",
				PathTpl: "/guarded",
			},
			When: []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
		}},
	}}))
	root.SetArgs([]string{"deploy", "--kind", "cpu"})
	testutil.NoError(t, root.Execute())
	summary := decodeWorkflowSummary(t, stdout.String())
	if got := stepStatuses(summary); !reflect.DeepEqual(got, map[string]string{"guarded": "skipped"}) {
		t.Fatalf("step statuses = %#v", got)
	}
}

func TestBuildWorkflows_BranchConvergenceSkipsConvergingStep(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := workflowServer(t, &paths, `{"id":"abc"}`)

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{
			{
				ID:        "gpu",
				Operation: publicGetSpec("gpu", "/gpu"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
			},
			{
				ID:        "cpu",
				Operation: publicGetSpec("cpu", "/cpu"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"cpu"}}},
			},
			{
				ID: "notify",
				Operation: CommandSpec{
					Group:    "System",
					Use:      "notify",
					Method:   "GET",
					PathTpl:  "/notify/{id}",
					Params:   []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true}},
					Security: &SecurityHint{Public: true},
				},
				Params: map[string]string{"id": "${steps.gpu.id}"},
			},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, reflect.DeepEqual(paths, []string{"/cpu"}), "paths = %#v, want the cpu branch only", paths)
	if got := stepStatuses(decodeWorkflowSummary(t, stdout.String()))["notify"]; got != "skipped" {
		t.Fatalf("notify status = %q, want skipped", got)
	}
}

func TestBuildWorkflows_RunningStepLoadsAuth(t *testing.T) {
	isolateRuntime(t)

	root := newWorkflowRoot(io.Discard)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{{
			ID: "guarded",
			Operation: CommandSpec{
				Group:   "System",
				Use:     "guarded",
				Method:  "GET",
				PathTpl: "/guarded",
			},
			When: []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
		}},
	}}))
	root.SetArgs([]string{"deploy", "--kind", "gpu"})
	err := root.Execute()
	testutil.Require(t, errors.Is(err, ErrNotAuthenticated), "error = %v, want ErrNotAuthenticated once the step actually runs", err)
}
