package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildWorkflows_ExecutesStepsWithReferences(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"tenant":"tenant 1"}`))
		case "/tenants/tenant 1/check":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := newWorkflowRoot(&bytes.Buffer{})
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use:   "doctor",
		Short: "Check API health",
		Steps: []WorkflowStepSpec{
			{
				ID: "health",
				Operation: CommandSpec{
					Group:    "System",
					Use:      "get-health",
					Method:   "GET",
					PathTpl:  "/health",
					Security: &SecurityHint{Public: true},
				},
			},
			{
				ID: "tenant",
				Operation: CommandSpec{
					Group:   "Tenants",
					Use:     "check-tenant",
					Method:  "GET",
					PathTpl: "/tenants/{tenant}/check",
					Params: []ParamSpec{
						{Name: "tenant", Flag: "tenant", In: InPath, GoType: "string", Required: true},
					},
					Security: &SecurityHint{Public: true},
				},
				Params: map[string]string{"tenant": "${steps.health.tenant}"},
			},
		},
		OutputFrom: "${steps.tenant}",
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"--hostname", srv.URL, "doctor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"ok":true}` {
		t.Fatalf("stdout = %q", stdout.String())
	}
	want := []string{"GET /health", "GET /tenants/tenant%201/check"}
	if strings.Join(requests, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestBuildWorkflows_StopsOnFailedStep(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/first" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.Error(w, `{"error":"down"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use: "doctor",
		Steps: []WorkflowStepSpec{
			{ID: "first", Operation: publicGetSpec("first", "/first")},
			{ID: "second", Operation: publicGetSpec("second", "/second")},
			{ID: "third", Operation: publicGetSpec("third", "/third")},
		},
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "doctor"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected workflow error")
	}
	var workflowErr *WorkflowError
	if !errors.As(err, &workflowErr) {
		t.Fatalf("error = %T %v, want WorkflowError", err, err)
	}
	if workflowErr.StepID != "second" {
		t.Fatalf("failed step = %q", workflowErr.StepID)
	}
	for _, path := range paths {
		if path == "/third" {
			t.Fatalf("third step ran: paths = %#v", paths)
		}
	}
	if len(paths) < 2 {
		t.Fatalf("paths = %#v, want first and second attempts", paths)
	}
}

func TestBuildWorkflows_RejectsInvalidInputEnum(t *testing.T) {
	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use: "doctor",
		Params: []ParamSpec{{
			Name:   "mode",
			Flag:   "mode",
			In:     InInput,
			GoType: "string",
			Enum:   []string{"quick", "full"},
		}},
		Steps: []WorkflowStepSpec{{ID: "health", Operation: publicGetSpec("health", "/health")}},
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", "http://127.0.0.1:1", "doctor", "--mode", "broken"})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), `invalid value "broken" for --mode`) {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildWorkflows_PreservesTypedParamReference(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var tags []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tags = r.URL.Query()["tag"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{{
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
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "doctor", "--tags", "a", "--tags", "b"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !reflect.DeepEqual(tags, []string{"a", "b"}) {
		t.Fatalf("tags = %#v", tags)
	}
}

func newWorkflowRoot(out io.Writer) *cobra.Command {
	root := newRootWithModuleGroup()
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	return root
}

func publicGetSpec(use string, path string) CommandSpec {
	return CommandSpec{
		Group:    "System",
		Use:      use,
		Method:   "GET",
		PathTpl:  path,
		Security: &SecurityHint{Public: true},
	}
}

func TestBuildWorkflows_WhenSelectsBranch(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	if err := BuildWorkflows(root, []WorkflowSpec{{
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
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"/cpu"}) {
		t.Fatalf("paths = %#v, want only /cpu", paths)
	}
	summary := decodeWorkflowSummary(t, stdout.String())
	if summary.Status != "ok" {
		t.Fatalf("status = %q", summary.Status)
	}
	if got := stepStatuses(summary); !reflect.DeepEqual(got, map[string]string{"gpu": "skipped", "cpu": "ok"}) {
		t.Fatalf("step statuses = %#v", got)
	}
}

// notin [""] is the documented existence check for an optional flag.
func TestBuildWorkflows_WhenTreatsUnsetInputAsEmpty(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "label", Flag: "label", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{{
			ID:        "label",
			Operation: publicGetSpec("label", "/label"),
			When:      []WorkflowCondition{{Value: "${input.label}", Operator: "notin", Values: []string{""}}},
		}},
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "deploy"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths = %#v, want no request when --label is unset", paths)
	}
}

func TestBuildWorkflows_SkipPropagatesThroughParamsAndConditions(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{
			{
				ID:        "gpu",
				Operation: publicGetSpec("gpu", "/gpu"),
				When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
			},
			// references a skipped step through params
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
			// references a skipped step from inside when itself
			{
				ID:        "audit",
				Operation: publicGetSpec("audit", "/audit"),
				When:      []WorkflowCondition{{Value: "${steps.gpu.id}", Operator: "in", Values: []string{"abc"}}},
			},
			// transitive: depends on a step that was itself skipped by propagation
			{
				ID:        "trail",
				Operation: publicGetSpec("trail", "/trail"),
				When:      []WorkflowCondition{{Value: "${steps.audit.ok}", Operator: "in", Values: []string{"true"}}},
			},
		},
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths = %#v, want every step skipped", paths)
	}
	summary := decodeWorkflowSummary(t, stdout.String())
	if summary.Status != "ok" {
		t.Fatalf("status = %q, want ok", summary.Status)
	}
	want := map[string]string{"gpu": "skipped", "notify": "skipped", "audit": "skipped", "trail": "skipped"}
	if got := stepStatuses(summary); !reflect.DeepEqual(got, want) {
		t.Fatalf("step statuses = %#v", got)
	}
}

// A skipped step must not load host options or refresh credentials. The step
// below is non-public and no host is configured, so reaching auth would fail.
func TestBuildWorkflows_SkippedStepDoesNotLoadAuth(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	if err := BuildWorkflows(root, []WorkflowSpec{{
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
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"deploy", "--kind", "cpu"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v, want the guarded step to be skipped before auth", err)
	}
	summary := decodeWorkflowSummary(t, stdout.String())
	if got := stepStatuses(summary); !reflect.DeepEqual(got, map[string]string{"guarded": "skipped"}) {
		t.Fatalf("step statuses = %#v", got)
	}
}

func TestBuildWorkflows_OutputFromSkippedStepDegradesToSummary(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	if err := BuildWorkflows(root, []WorkflowSpec{{
		Use:    "deploy",
		Params: []ParamSpec{{Name: "kind", Flag: "kind", In: InInput, GoType: "string"}},
		Steps: []WorkflowStepSpec{{
			ID:        "gpu",
			Operation: publicGetSpec("gpu", "/gpu"),
			When:      []WorkflowCondition{{Value: "${input.kind}", Operator: "in", Values: []string{"gpu"}}},
		}},
		OutputFrom: "${steps.gpu}",
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	summary := decodeWorkflowSummary(t, stdout.String())
	if summary.Status != "ok" || len(summary.Steps) != 1 || summary.Steps[0].Status != "skipped" {
		t.Fatalf("summary = %#v", summary)
	}
}

// Branch convergence is a documented limitation: a step that references one
// branch is skipped when the other branch runs. See docs/workflow-conditional.md.
func TestBuildWorkflows_BranchConvergenceSkipsConvergingStep(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	if err := BuildWorkflows(root, []WorkflowSpec{{
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
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"--hostname", srv.URL, "deploy", "--kind", "cpu"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"/cpu"}) {
		t.Fatalf("paths = %#v, want the cpu branch only", paths)
	}
	if got := stepStatuses(decodeWorkflowSummary(t, stdout.String()))["notify"]; got != "skipped" {
		t.Fatalf("notify status = %q, want skipped", got)
	}
}

func decodeWorkflowSummary(t *testing.T, raw string) WorkflowResult {
	t.Helper()
	var summary WorkflowResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", raw, err)
	}
	return summary
}

func stepStatuses(result WorkflowResult) map[string]string {
	out := map[string]string{}
	for _, step := range result.Steps {
		out[step.ID] = step.Status
	}
	return out
}

// Control for TestBuildWorkflows_SkippedStepDoesNotLoadAuth: with the condition
// satisfied, the same step reaches auth and fails. The pair pins the ordering.
func TestBuildWorkflows_RunningStepLoadsAuth(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{{
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
	}}); err != nil {
		t.Fatalf("BuildWorkflows: %v", err)
	}
	root.SetArgs([]string{"deploy", "--kind", "gpu"})
	err := root.Execute()
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("error = %v, want ErrNotAuthenticated once the step actually runs", err)
	}
}
