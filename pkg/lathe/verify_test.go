package lathe

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestRunVerifyGeneratedJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n  short: test cli\n"),
		Mount: func(root *cobra.Command) error {
			if err := runtime.Build(root, "demo", []runtime.CommandSpec{
				{
					Group:   "Users",
					Use:     "get-user",
					Short:   "Get a user",
					Method:  "GET",
					PathTpl: "/users/{id}",
					Params: []runtime.ParamSpec{{
						Name:     "id",
						Flag:     "id",
						In:       runtime.InPath,
						GoType:   "string",
						Required: true,
					}},
				},
				{
					Group:       "Users",
					Use:         "create-user",
					Short:       "Create a user",
					Method:      "POST",
					PathTpl:     "/users",
					RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json"},
				},
			}); err != nil {
				return err
			}
			skills := &cobra.Command{Use: "skills"}
			pkg := &cobra.Command{Use: "package"}
			pkg.Flags().String("file", "", "")
			pkg.Flags().String("github-url", "", "")
			pkg.Flags().String("app-id", "", "")
			pkg.Flags().String("skill-id", "", "")
			runtime.AttachCatalogCommand(pkg, "console-rest", runtime.CommandSpec{
				Group:       "Skills",
				Use:         "package",
				Short:       "Package skill",
				Method:      "POST",
				PathTpl:     "/skills/package",
				RequestBody: &runtime.RequestBody{Required: true, MediaType: "multipart/form-data"},
				Params: []runtime.ParamSpec{
					{Name: "file", Flag: "file", In: runtime.InFormData, GoType: "string", Required: true},
					{Name: "githubUrl", Flag: "github-url", In: runtime.InFormData, GoType: "string", Required: true},
					{Name: "appId", Flag: "app-id", In: runtime.InFormData, GoType: "string", Required: true},
					{Name: "skillId", Flag: "skill-id", In: runtime.InFormData, GoType: "string", Required: true},
				},
			})
			skills.AddCommand(pkg)
			root.AddCommand(skills)
			return nil
		},
	}, []string{"__lathe", "verify", "--json"}, &stdout, &stderr)
	if code != runtime.ExitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var report verifyReport
	testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	testutil.Require(t, report.OK, "report = %+v", report)
	testutil.Require(t, report.Version == 2, "version = %d, want 2", report.Version)
	testutil.Require(t, report.Provenance.SchemaVersion == runtime.SchemaVersion, "schema_version = %d, want %d", report.Provenance.SchemaVersion, runtime.SchemaVersion)
	testutil.Require(t, report.Provenance.CatalogSchemaVersion == runtime.CatalogSchemaVersion, "catalog_schema_version = %d, want %d", report.Provenance.CatalogSchemaVersion, runtime.CatalogSchemaVersion)
	testutil.Require(t, report.Provenance.Sources != nil && len(report.Provenance.Sources) == 0, "sources = %#v, want empty slice", report.Provenance.Sources)
	testutil.Require(t, strings.Contains(stdout.String(), `"sources": []`), "json missing empty sources:\n%s", stdout.String())
	for _, want := range []string{
		"root_help",
		"commands_schema",
		"commands_json",
		"catalog_nonempty",
		"commands_show:demo users get-user",
		"commands_show:demo users create-user",
		"commands_show:skills package",
		"auth_status_unauthenticated",
	} {
		testutil.Require(t, verifyReportHasCheck(report, want), "report missing %q: %+v", want, report.Checks)
	}
}

func TestVerifyGeneratedSkillInstall(t *testing.T) {
	root := NewApp(testManifest())
	testutil.NoError(t, runtime.Build(root, "demo", []runtime.CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}}))
	runtime.AttachCapability(root, runtime.CapabilitySkillBundle)
	skill := &cobra.Command{Use: "skill"}
	hookRan := false
	install := &cobra.Command{
		Use: "install",
		PreRunE: func(_ *cobra.Command, _ []string) error {
			hookRan = true
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			if !hookRan {
				return errors.New("pre-run hook did not run")
			}
			target := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "myctl")
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("skill"), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(target, ".kitup.json"), []byte("{}"), 0o644)
		},
	}
	install.Flags().String("scope", "", "")
	install.Flags().String("agent", "", "")
	install.Flags().Bool("yes", false, "")
	skill.AddCommand(install)
	root.AddCommand(skill)

	report := verifyGenerated(root, testManifest())
	testutil.Require(t, verifyReportHasCheck(report, "skill_install"), "report missing skill_install: %+v", report.Checks)
	testutil.Require(t, hookRan, "skill install hook did not run")
}

func TestVerifyGeneratedHiddenWorkflowContract(t *testing.T) {
	root := NewApp(testManifest())
	testutil.NoError(t, runtime.Build(root, "demo", []runtime.CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}}))
	testutil.NoError(t, runtime.BuildWorkflows(root, []runtime.WorkflowSpec{{
		Use:    "doctor",
		Hidden: true,
		Steps: []runtime.WorkflowStepSpec{{
			ID: "health",
			Operation: runtime.CommandSpec{
				OperationID: "Health_Check",
				Method:      "GET",
				PathTpl:     "/health",
				Security:    &runtime.SecurityHint{Public: true},
			},
		}},
	}}))

	report := verifyGenerated(root, testManifest())
	testutil.Require(t, report.OK, "report = %+v", report)
	testutil.Require(t, verifyReportHasCheck(report, "workflow_contract"), "report missing workflow_contract: %+v", report.Checks)
}

func TestRunVerifyGeneratedFailureReturnsJSONOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n"),
		Mount: func(root *cobra.Command) error {
			bad := &cobra.Command{Use: "bad"}
			runtime.AttachCatalogCommand(bad, "demo", runtime.CommandSpec{
				Use: "bad",
				Params: []runtime.ParamSpec{{
					Name:     "id",
					Flag:     "id",
					In:       runtime.InPath,
					GoType:   "string",
					Required: true,
				}},
			})
			root.AddCommand(bad)
			return nil
		},
	}, []string{"__lathe", "verify", "--json"}, &stdout, &stderr)
	testutil.Require(t, code == runtime.ExitGeneral, "exit = %d, want %d", code, runtime.ExitGeneral)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var report verifyReport
	testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	testutil.Require(t, !report.OK, "report unexpectedly passed: %+v", report)
	if !strings.Contains(stdout.String(), "missing --id") {
		t.Fatalf("report missing flag failure:\n%s", stdout.String())
	}
}

func TestRunVerifyGeneratedProvenanceJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n  short: test cli\n"),
		Version:  "dev",
		Commit:   "abc123",
		Mount: func(root *cobra.Command) error {
			if err := runtime.Build(root, "demo", []runtime.CommandSpec{{
				Group:   "Users",
				Use:     "get-user",
				Short:   "Get a user",
				Method:  "GET",
				PathTpl: "/users/{id}",
				Params: []runtime.ParamSpec{{
					Name:     "id",
					Flag:     "id",
					In:       runtime.InPath,
					GoType:   "string",
					Required: true,
				}},
			}}); err != nil {
				return err
			}
			runtime.AttachSourceProvenance(root, []runtime.SourceProvenance{
				{ID: "pets", Backend: "openapi3", Kind: "git", RepoURL: "https://example.com/petstore.git", PinnedTag: "v1.0.0", ResolvedSHA: "def456", Reproducible: true},
				{ID: "local", Backend: "openapi3", Kind: "local", Reproducible: false},
			})
			return nil
		},
	}, []string{"__lathe", "verify", "--json"}, &stdout, &stderr)
	if code != runtime.ExitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	var report verifyReport
	testutil.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	testutil.Require(t, report.OK && report.Version == 2, "report = %+v", report)
	testutil.Require(t, report.Provenance.CLI.Name == "myctl", "cli name = %q", report.Provenance.CLI.Name)
	testutil.Require(t, report.Provenance.CLI.Version == "dev", "cli version = %q", report.Provenance.CLI.Version)
	testutil.Require(t, report.Provenance.CLI.Commit == "abc123", "cli commit = %q", report.Provenance.CLI.Commit)
	testutil.Require(t, len(report.Provenance.Sources) == 2, "sources = %#v", report.Provenance.Sources)
	git := report.Provenance.Sources[0]
	testutil.Require(t, git.Kind == "git" && git.RepoURL == "https://example.com/petstore.git" && git.ResolvedSHA == "def456" && git.Reproducible, "git source = %+v", git)
	local := report.Provenance.Sources[1]
	testutil.Require(t, local.Kind == "local" && local.RepoURL == "" && local.ResolvedSHA == "" && !local.Reproducible, "local source = %+v", local)
	raw := stdout.String()
	localJSON := raw[strings.LastIndex(raw, `"id": "local"`):]
	testutil.Require(t, !strings.Contains(localJSON, `"repo_url"`) && !strings.Contains(localJSON, `"resolved_sha"`), "local source leaked identity fields:\n%s", localJSON)
}

func TestRunVerifyGeneratedHumanOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(RunOptions{
		Manifest: []byte("cli:\n  name: myctl\n  short: test cli\n"),
		Version:  "dev",
		Commit:   "abc123",
		Mount: func(root *cobra.Command) error {
			if err := runtime.Build(root, "demo", []runtime.CommandSpec{{
				Group:   "Users",
				Use:     "get-user",
				Short:   "Get a user",
				Method:  "GET",
				PathTpl: "/users/{id}",
				Params: []runtime.ParamSpec{{
					Name:     "id",
					Flag:     "id",
					In:       runtime.InPath,
					GoType:   "string",
					Required: true,
				}},
			}}); err != nil {
				return err
			}
			runtime.AttachSourceProvenance(root, []runtime.SourceProvenance{
				{ID: "pets", Backend: "openapi3", Kind: "git", RepoURL: "https://example.com/petstore.git", PinnedTag: "v1.0.0", ResolvedSHA: "def456", Reproducible: true},
				{ID: "local", Backend: "openapi3", Kind: "local", Reproducible: false},
			})
			return nil
		},
	}, []string{"__lathe", "verify"}, &stdout, &stderr)
	if code != runtime.ExitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	got := stdout.String()
	testutil.Require(t, !json.Valid(bytes.TrimSpace(stdout.Bytes())), "human output was JSON:\n%s", got)
	for _, want := range []string{
		"CLI: myctl dev (commit abc123)",
		"Source pets: openapi3 git https://example.com/petstore.git v1.0.0 @ def456",
		"not reproducible",
	} {
		testutil.Require(t, strings.Contains(got, want), "human output missing %q:\n%s", want, got)
	}
}

func verifyReportHasCheck(report verifyReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.OK {
			return true
		}
	}
	return false
}
