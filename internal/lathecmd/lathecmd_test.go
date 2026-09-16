package lathecmd

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestRunWithOutput_RootHelpPrintsSubcommands(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"-h"}, &out)
	testutil.Require(t, errors.Is(err, flag.ErrHelp), "expected flag.ErrHelp, got %v", err)
	got := out.String()
	for _, want := range []string{"Usage:", "lathe specsync", "lathe codegen", "lathe bootstrap", "lathe version"} {
		testutil.Require(t, strings.Contains(got, want), "help output missing %q:\n%s", want, got)
	}
}

func TestRunWithOutput_VersionPrintsLatheVersion(t *testing.T) {
	var out bytes.Buffer
	testutil.NoError(t, RunWithOutput([]string{"version"}, &out))
	if got := out.String(); !strings.HasPrefix(got, "lathe ") {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestRunWithOutputs_VersionUsesStdout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	testutil.NoError(t, runWithOutputs([]string{"version"}, &stdout, &stderr))
	if got := stdout.String(); !strings.HasPrefix(got, "lathe ") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr should be empty, got %q", got)
	}
}

func TestRunWithOutput_SubcommandHelp(t *testing.T) {
	for _, tc := range []struct {
		command string
		flags   []string
	}{
		{"codegen", []string{"-manifest", "-skill-root", "-skill-include"}},
		{"specsync", []string{"-source", "-sources"}},
		{"bootstrap", []string{"-manifest", "-skill-root", "-skill-include"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var out bytes.Buffer
			if err := RunWithOutput([]string{tc.command, "-h"}, &out); !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("expected flag.ErrHelp, got %v", err)
			}
			for _, want := range append(tc.flags, "Usage of lathe "+tc.command+":") {
				testutil.Require(t, strings.Contains(out.String(), want), "help output missing %q:\n%s", want, &out)
			}
		})
	}
}

func TestRunWithOutput_UnknownCommandFails(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"unknown"}, &out)
	testutil.Require(t, err != nil, "expected unknown command error")
	testutil.Require(t, strings.Contains(err.Error(), `unknown command "unknown"`), "unexpected error: %v", err)
}

func TestRunCodegen_GeneratesWorkflowCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
workflow:
  commands:
    - use: doctor
      short: Check API health
      steps:
        - id: users
          uses: acme.Users_List
      output:
        from: ${steps.users}
`)

	testutil.NoError(t, runTestCodegen())

	workflows := strings.Join(strings.Fields(readCodegenFile(t, "internal/generated/workflows/workflows_gen.go")), " ")
	for _, want := range []string{`Use: "doctor"`, `ID: "users"`, `OperationID: "Users_List"`} {
		testutil.Require(t, strings.Contains(workflows, want), "workflows_gen missing %q:\n%s", want, workflows)
	}
	modulesGen := readCodegenFile(t, "internal/generated/modules_gen.go")
	testutil.Require(t, strings.Contains(modulesGen, "lathegeneratedworkflows.Mount(root)"), "modules_gen should mount workflows:\n%s", modulesGen)
}

func TestRunCodegen_RejectsInvalidWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name, command, want string
	}{
		{"unknown step param", `    - use: doctor
      steps:
        - id: users
          uses: acme.Users_List
          params:
            missing: value
`, `param "missing"`},
		{"malformed reference", `    - use: doctor
      inputs:
        - name: tenant_id
      steps:
        - id: users
          uses: acme.Users_List
      output:
        from: ${input.tenant_id
`, "unterminated reference"},
		{"alias root conflict", `    - use: doctor
      aliases: [users]
      steps:
        - id: users
          uses: acme.Users_List
`, `alias "users"`},
		{"reserved alias", `    - use: doctor
      aliases: [auth]
      steps:
        - id: users
          uses: acme.Users_List
`, "reserved root command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			seedCodegenProject(t, true)
			writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\nworkflow:\n  commands:\n"+tc.command)
			if err := runTestCodegen(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunCodegen_CommandPathFlatRewritesGeneratedExamples(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "overlays/acme.yaml", `commands:
  list:
    example: "acmectl acme users list -o json"
`)

	testutil.NoError(t, RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-overlay", "overlays"}, &bytes.Buffer{}))

	generated := readCodegenFile(t, "internal/generated/acme/acme_gen.go")
	testutil.Require(t, !strings.Contains(generated, `"acmectl acme users list -o json"`), "generated catalog example kept namespaced path:\n%s", generated)
	testutil.Require(t, strings.Contains(generated, `Example:     "acmectl users list -o json"`), "generated catalog example should use flat path:\n%s", generated)
}

func TestRunBootstrapSyncsAndGenerates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for bootstrap")
	}

	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	writeCodegenFile(t, filepath.Join(upstream, "openapi.yaml"), `openapi: "3.0.3"
paths:
  /users:
    get:
      operationId: Users_List
      tags: [Users]
      summary: List users
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
`)
	runGit(t, upstream, "init")
	runGit(t, upstream, "add", ".")
	runGit(t, upstream, "-c", "user.name=Lathe", "-c", "user.email=lathe@example.com", "commit", "-m", "initial")
	runGit(t, upstream, "tag", "v1.0.0")

	project := filepath.Join(root, "project")
	testutil.NoError(t, os.MkdirAll(project, 0o755))
	t.Chdir(project)
	writeCodegenFile(t, "go.mod", "module example.com/fake\n\ngo 1.25\n")
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\n")
	writeCodegenFile(t, "specs/sources.yaml", `sources:
  acme:
    repo_url: `+upstream+`
    pinned_tag: v1.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
`)

	testutil.NoError(t, RunBootstrap([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{}))

	for _, path := range []string{
		".cache/specs-sync/acme/sync-state.yaml",
		"internal/generated/acme/acme_gen.go",
		"internal/generated/modules_gen.go",
		"skills/acmectl/SKILL.md",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
}

func TestRunBootstrapLocalPathSyncsAndGenerates(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeCodegenFile(t, "go.mod", "module example.com/fake\n\ngo 1.25\n")
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\n")
	writeCodegenFile(t, "api/openapi.yaml", `openapi: "3.0.3"
paths:
  /users:
    get:
      operationId: Users_List
      tags: [Users]
      summary: List users
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
`)
	writeCodegenFile(t, "specs/sources.yaml", `sources:
  acme:
    local_path: ../api
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
`)

	testutil.NoError(t, RunBootstrap([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{}))

	localPath, err := filepath.Abs("api")
	testutil.Require(t, err == nil, "%v", err)
	state, err := os.ReadFile(".cache/specs-sync/acme/sync-state.yaml")
	testutil.Require(t, err == nil, "read sync state: %v", err)
	testutil.Require(t, strings.Contains(string(state), "source_kind: local") && strings.Contains(string(state), "synced_from: "+localPath), "sync state = %s, want local source path %q", state, localPath)
	for _, path := range []string{"internal/generated/acme/acme_gen.go", "skills/acmectl/SKILL.md"} {
		data, err := os.ReadFile(path)
		testutil.Require(t, err == nil, "read %s: %v", path, err)
		testutil.Require(t, !strings.Contains(string(data), localPath), "%s encoded local path %q", path, localPath)
	}

	writeCodegenFile(t, "other/openapi.yaml", `openapi: "3.0.3"
paths: {}
`)
	writeCodegenFile(t, "specs/sources.yaml", `sources:
  acme:
    local_path: ../other
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
`)
	err = runTestCodegen()
	testutil.Require(t, err != nil, "codegen accepted stale local_path cache")
	testutil.Require(t, strings.Contains(err.Error(), "local_path"), "error = %v, want local_path mismatch", err)
}

func TestRunCodegen_CommandPathNamespacedKeepsModuleSegment(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\n  command_path: namespaced\n")
	writeCodegenFile(t, ".cache/specs-sync/acme/openapi.yaml", `openapi: "3.0.3"
paths:
  /payments:
    get:
      operationId: PaymentAPI_List
      tags: ["Payment API"]
      summary: List payments
      responses:
        "200":
          description: OK
`)
	writeCodegenFile(t, "overlays/acme.yaml", `commands:
  list:
    example: "acmectl acme payment api list -o json"
`)

	testutil.NoError(t, RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-overlay", "overlays"}, &bytes.Buffer{}))

	modulesGen := readCodegenFile(t, "internal/generated/modules_gen.go")
	testutil.Require(t, !strings.Contains(modulesGen, "MountFlat"), "namespaced command_path should not flat mount:\n%s", modulesGen)
	generated := readCodegenFile(t, "internal/generated/acme/acme_gen.go")
	testutil.Require(t, !strings.Contains(generated, `"acmectl acme payment api list -o json"`), "generated catalog example kept unnormalized path:\n%s", generated)
	testutil.Require(t, strings.Contains(generated, `Example:     "acmectl acme payment list -o json"`), "generated catalog example should use namespaced Cobra path:\n%s", generated)
	module := readCodegenFile(t, "skills/acmectl/references/modules/acme.md")
	testutil.Require(t, strings.Contains(module, "`acmectl acme payment list`"), "module reference should keep module path:\n%s", module)
	testutil.Require(t, !strings.Contains(module, "payment api list"), "module reference kept unnormalized path:\n%s", module)
}

func TestRunCodegen_CommandPathFlatRejectsRootConflict(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\n  command_path: flat\n")
	writeCodegenFile(t, ".cache/specs-sync/acme/openapi.yaml", `openapi: "3.0.3"
paths:
  /query:
    get:
      operationId: Search_Query
      tags: [Search]
      summary: Query search
      responses:
        "200":
          description: OK
`)

	err := runTestCodegen()
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "conflicts"), "expected flat conflict error, got %v", err)
	if _, err := os.Stat("internal/generated/acme/acme_gen.go"); !os.IsNotExist(err) {
		t.Fatalf("conflicting flat config should fail before writing module output, stat err = %v", err)
	}
}

func TestRunCodegen_RejectsReservedModuleNameBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeCodegenFile(t, "go.mod", "module example.com/fake\n\ngo 1.25\n")
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\n  command_path: namespaced\n")
	writeCodegenFile(t, "specs/sources.yaml", `sources:
  auth:
    repo_url: https://example.com/acme.git
    pinned_tag: v1.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
`)
	writeCodegenFile(t, ".cache/specs-sync/auth/sync-state.yaml", `source: auth
backend: openapi3
synced_from: v1.0.0
resolved_sha: "0000000000000000000000000000000000000000"
`)
	writeCodegenFile(t, ".cache/specs-sync/auth/openapi.yaml", `openapi: "3.0.3"
paths:
  /users:
    get:
      operationId: Users_List
      tags: [Users]
      summary: List users
      responses:
        "200":
          description: OK
`)

	err := runTestCodegen()
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "reserved root command"), "expected reserved module name error, got %v", err)
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
}

func seedCodegenProject(t *testing.T, withManifest bool) {
	t.Helper()
	writeCodegenFile(t, "go.mod", "module example.com/fake\n\ngo 1.25\n")
	if withManifest {
		writeCodegenFile(t, "cli.yaml", "cli:\n  name: acmectl\n  short: Acme CLI\n")
	}
	writeCodegenFile(t, "specs/sources.yaml", `sources:
  acme:
    repo_url: https://example.com/acme.git
    pinned_tag: v1.0.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
`)
	writeCodegenFile(t, ".cache/specs-sync/acme/sync-state.yaml", `source: acme
backend: openapi3
synced_from: v1.0.0
resolved_sha: "0000000000000000000000000000000000000000"
`)
	writeCodegenFile(t, ".cache/specs-sync/acme/openapi.yaml", `openapi: "3.0.3"
paths:
  /users:
    get:
      operationId: Users_List
      tags: [Users]
      summary: List users
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  items:
                    type: array
                    items:
                      type: object
`)
}

func writeCodegenFile(t *testing.T, path string, body string) {
	t.Helper()
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func readCodegenFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	testutil.Require(t, err == nil, "read %s: %v", path, err)
	return string(data)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runTestCodegen(args ...string) error {
	return RunCodegen(append([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, args...), &bytes.Buffer{})
}
