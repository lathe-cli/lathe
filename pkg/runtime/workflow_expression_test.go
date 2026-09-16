package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuildWorkflows_PreservesTypedParamReference(t *testing.T) {
	isolateRuntime(t)

	var tags []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tags = r.URL.Query()["tag"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	root := newWorkflowRoot(io.Discard)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "doctor",
		Params: []ParamSpec{{
			Name:   "tags",
			Flag:   "tags",
			In:     InInput,
			GoType: "[]string",
		}},
		Steps: []WorkflowStepSpec{{
			ID: "check",
			Operation: CommandSpec{
				Group:   "System",
				Use:     "check",
				Method:  "GET",
				PathTpl: "/check",
				Params: []ParamSpec{{
					Name:   "tag",
					Flag:   "tag",
					In:     InQuery,
					GoType: "[]string",
				}},
				Security: &SecurityHint{Public: true},
			},
			Params: map[string]string{"tag": "${input.tags}"},
		}},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "doctor", "--tags", "a", "--tags", "b"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, reflect.DeepEqual(tags, []string{"a", "b"}), "tags = %#v", tags)
}

func TestBuildWorkflows_OutputFromSkippedStepDegradesToSummary(t *testing.T) {
	isolateRuntime(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{{
			ID:        "gpu",
			Operation: publicGetSpec("gpu", "/gpu"),
			When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
		}},
		OutputFrom: "${steps.gpu}",
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	testutil.NoError(t, root.Execute())
	summary := decodeWorkflowSummary(t, stdout.String())
	testutil.Require(t, summary.Status == "ok" && len(summary.Steps) == 1 && summary.Steps[0].Status == "skipped", "summary = %#v", summary)
}

func TestBuildWorkflows_ConditionKeepsLiteralAroundMissingReference(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := workflowServer(t, &paths, `{"present":"here"}`)

	root := newWorkflowRoot(io.Discard)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "deploy",
		Steps: []WorkflowStepSpec{
			{ID: "probe", Operation: publicGetSpec("probe", "/probe")},
			{
				ID:        "guarded",
				Operation: publicGetSpec("guarded", "/guarded"),
				When: []WorkflowCondition{{
					Value:    "prefix-${steps.probe.missing}",
					Operator: "in",
					Values:   []string{"prefix-"},
				}},
			},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, reflect.DeepEqual(paths, []string{"/probe", "/guarded"}), "paths = %#v, want the guarded step to run", paths)
}

func TestBuildWorkflows_PreservesNumericReferenceSemantics(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/probe" {
			_, _ = w.Write([]byte(`{"id":9007199254740993,"decimal":1.0,"exponent":1e3}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	root := newWorkflowRoot(io.Discard)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "deploy",
		Steps: []WorkflowStepSpec{
			{ID: "probe", Operation: publicGetSpec("probe", "/probe")},
			{
				ID: "fetch",
				Operation: CommandSpec{
					Use:      "fetch",
					Method:   "GET",
					PathTpl:  "/items/{id}",
					Params:   []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "int64", Required: true}},
					Security: &SecurityHint{Public: true},
				},
				When: []WorkflowCondition{
					{Value: "${steps.probe.id}", Operator: "in", Values: []string{"9007199254740993"}},
					{Value: "${steps.probe.decimal}", Operator: "in", Values: []string{"1"}},
					{Value: "${steps.probe.exponent}", Operator: "in", Values: []string{"1000"}},
				},
				Params: map[string]string{"id": "${steps.probe.id}"},
			},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy"})
	testutil.NoError(t, root.Execute())
	want := []string{"/probe", "/items/9007199254740993"}
	testutil.Require(t, reflect.DeepEqual(paths, want), "paths = %#v, want %#v", paths, want)
}

func TestBuildWorkflows_CompositeOutputNullsSkippedStep(t *testing.T) {
	isolateRuntime(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pod":
			_, _ = w.Write([]byte(`{"name":"web-0","node":""}`))
		case "/events":
			_, _ = w.Write([]byte(`{"items":[{"reason":"FailedScheduling"}]}`))
		case "/neighbors":
			_, _ = w.Write([]byte(`{"items":[]}`))
		}
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "diag",
		Params: []ParamSpec{
			{Name: "kind", Flag: "kind", In: InInput, GoType: "string"},
		},
		Steps: []WorkflowStepSpec{
			{ID: "pod", Operation: publicGetSpec("get-pod", "/pod")},
			{ID: "events", Operation: publicGetSpec("list-events", "/events")},
			{
				ID:        "neighbors",
				Operation: publicGetSpec("list-neighbors", "/neighbors"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"has-node"}}},
			},
		},
		OutputFrom: `{"pod":${steps.pod},"events":${steps.events},"neighbors":${steps.neighbors}}`,
	}}))

	root.SetArgs([]string{"--hostname", srv.URL, "diag", "--kind", "no-node"})
	testutil.NoError(t, root.Execute())

	raw := strings.TrimSpace(stdout.String())

	var outer string
	testutil.NoError(t, json.Unmarshal([]byte(raw), &outer))
	var result map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(outer), &result))
	testutil.Require(t, result["pod"] != nil, "pod should not be nil")
	testutil.Require(t, result["events"] != nil, "events should not be nil")
	testutil.Require(t, result["neighbors"] == nil, "neighbors should be null, got %v", result["neighbors"])
}
