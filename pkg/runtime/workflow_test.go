package runtime

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuildWorkflows_ExecutesStepsWithReferences(t *testing.T) {
	isolateRuntime(t)

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
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
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
	}}))
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"--hostname", srv.URL, "doctor"})
	testutil.NoError(t, root.Execute())
	if strings.TrimSpace(stdout.String()) != `{"ok":true}` {
		t.Fatalf("stdout = %q", stdout.String())
	}
	want := []string{"GET /health", "GET /tenants/tenant%201/check"}
	testutil.Require(t, strings.Join(requests, "|") == strings.Join(want, "|"), "requests = %#v", requests)
}

func TestBuildWorkflows_StopsOnFailedStep(t *testing.T) {
	isolateRuntime(t)

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
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "doctor",
		Steps: []WorkflowStepSpec{
			{ID: "first", Operation: publicGetSpec("first", "/first")},
			{ID: "second", Operation: publicGetSpec("second", "/second")},
			{ID: "third", Operation: publicGetSpec("third", "/third")},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "doctor"})
	err := root.Execute()
	testutil.Require(t, err != nil, "expected workflow error")
	var workflowErr *WorkflowError
	testutil.Require(t, errors.As(err, &workflowErr), "error = %T %v, want WorkflowError", err, err)
	testutil.Require(t, workflowErr.StepID == "second", "failed step = %q", workflowErr.StepID)
	for _, path := range paths {
		testutil.Require(t, path != "/third", "third step ran: paths = %#v", paths)
	}
	testutil.Require(t, len(paths) >= 2, "paths = %#v, want first and second attempts", paths)
}

func TestBuildWorkflows_StopsOnPausedStream(t *testing.T) {
	isolateRuntime(t)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/pause" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"kind\":\"checkpoint\",\"token\":\"resume-1\"}\n\n")
			return
		}
		_, _ = w.Write([]byte(`{"unexpected":true}`))
	}))
	defer srv.Close()

	streaming := &StreamingHint{Strategy: "sse", Policy: &StreamPolicy{
		DataFormat: "json", EventNamePath: "kind",
		Collect: &StreamCollectHint{
			RequireStop: true,
			PauseEvents: []string{"checkpoint"},
			Fields: []StreamFieldRule{
				{Events: []string{"checkpoint"}, Value: "paused", To: "status", Reduce: "last"},
				{Events: []string{"checkpoint"}, From: "token", To: "resume_token", Reduce: "last"},
			},
		},
	}}
	var stdout bytes.Buffer
	root := newWorkflowRoot(&stdout)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "deploy",
		Steps: []WorkflowStepSpec{
			{ID: "wait", Operation: CommandSpec{Method: "GET", PathTpl: "/pause", Security: &SecurityHint{Public: true}, Output: OutputHints{Streaming: streaming}}},
			{ID: "after", Operation: publicGetSpec("after", "/after")},
		},
	}}))
	root.SetArgs([]string{"--hostname", srv.URL, "deploy"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, reflect.DeepEqual(paths, []string{"/pause"}), "paths = %#v", paths)
	if strings.TrimSpace(stdout.String()) != `{"resume_token":"resume-1","status":"paused"}` {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBuildWorkflows_RejectsCompletionNameAndAlias(t *testing.T) {
	spec := WorkflowSpec{
		Use:   "completion",
		Steps: []WorkflowStepSpec{{ID: "health", Operation: publicGetSpec("health", "/health")}},
	}
	root := newWorkflowRoot(io.Discard)
	if err := BuildWorkflows(root, []WorkflowSpec{spec}); err == nil || !strings.Contains(err.Error(), `workflow command "completion" conflicts`) {
		t.Fatalf("expected completion conflict error, got %v", err)
	}
	if len(root.Commands()) != 0 {
		t.Fatalf("completion workflow must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}

	aliased := spec
	aliased.Use = "doctor"
	aliased.Aliases = []string{"completion"}
	root = newWorkflowRoot(io.Discard)
	err := BuildWorkflows(root, []WorkflowSpec{aliased})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), `alias "completion" conflicts`), "expected completion alias conflict error, got %v", err)
}

func TestBuildWorkflows_RejectsInvalidInputEnum(t *testing.T) {
	root := newWorkflowRoot(io.Discard)
	testutil.NoError(t, BuildWorkflows(root, []WorkflowSpec{{
		Use: "doctor",
		Params: []ParamSpec{{
			Name:   "mode",
			Flag:   "mode",
			In:     InInput,
			GoType: "string",
			Enum:   []string{"quick", "full"},
		}},
		Steps: []WorkflowStepSpec{{ID: "health", Operation: publicGetSpec("health", "/health")}},
	}}))
	root.SetArgs([]string{"--hostname", "http://127.0.0.1:1", "doctor", "--mode", "broken"})

	err := root.Execute()
	testutil.Require(t, err != nil && strings.Contains(err.Error(), `invalid value "broken" for --mode`), "error = %v", err)
}
