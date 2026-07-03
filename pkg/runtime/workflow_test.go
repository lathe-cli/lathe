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
