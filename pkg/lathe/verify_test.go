package lathe

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/runtime"
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
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if !report.OK {
		t.Fatalf("report = %+v", report)
	}
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
		if !verifyReportHasCheck(report, want) {
			t.Fatalf("report missing %q: %+v", want, report.Checks)
		}
	}
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
	if code != runtime.ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, runtime.ExitGeneral)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var report verifyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if report.OK {
		t.Fatalf("report unexpectedly passed: %+v", report)
	}
	if !strings.Contains(stdout.String(), "missing --id") {
		t.Fatalf("report missing flag failure:\n%s", stdout.String())
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
