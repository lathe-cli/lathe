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
)

func TestRunWithOutput_RootHelpPrintsSubcommands(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"Usage:", "lathe specsync", "lathe codegen", "lathe bootstrap", "lathe version"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q:\n%s", want, got)
		}
	}
}

func TestRunWithOutput_VersionPrintsLatheVersion(t *testing.T) {
	var out bytes.Buffer
	if err := RunWithOutput([]string{"version"}, &out); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "lathe ") {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestRunWithOutputs_VersionUsesStdout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithOutputs([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := stdout.String(); !strings.HasPrefix(got, "lathe ") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr should be empty, got %q", got)
	}
}

func TestRunWithOutput_CodegenHelpPrintsUsage(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"codegen", "-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"Usage of lathe codegen:", "-manifest", "-skill-root", "-skill-include"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q:\n%s", want, got)
		}
	}
}

func TestRunWithOutput_SpecsyncHelpPrintsUsage(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"specsync", "-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"Usage of lathe specsync:", "-source", "-sources"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q:\n%s", want, got)
		}
	}
}

func TestRunWithOutput_BootstrapHelpPrintsUsage(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"bootstrap", "-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"Usage of lathe bootstrap:", "-manifest", "-skill-root", "-skill-include"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q:\n%s", want, got)
		}
	}
}

func TestRunWithOutput_UnknownCommandFails(t *testing.T) {
	var out bytes.Buffer
	err := RunWithOutput([]string{"unknown"}, &out)
	if err == nil {
		t.Fatal("expected unknown command error")
	}
	if !strings.Contains(err.Error(), `unknown command "unknown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_CodegenSubcommandGeneratesSkillDirectoryByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)

	if err := Run([]string{"codegen", "-sources", "specs/sources.yaml", "-cache", ".cache"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, path := range []string{
		"internal/generated/acme/acme_gen.go",
		"internal/generated/modules_gen.go",
		"skills/acmectl/SKILL.md",
		"skills/acmectl/agents/openai.yaml",
		"skills/acmectl/references/catalog.md",
		"skills/acmectl/references/modules/acme.md",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}

	skill := readCodegenFile(t, "skills/acmectl/SKILL.md")
	if !strings.Contains(skill, "acmectl search \"<intent>\" --json") {
		t.Fatalf("skill missing search workflow:\n%s", skill)
	}
	modulesGen := readCodegenFile(t, "internal/generated/modules_gen.go")
	if !strings.Contains(modulesGen, "acme.MountFlat(root)") {
		t.Fatalf("single-module default should flat mount:\n%s", modulesGen)
	}
	module := readCodegenFile(t, "skills/acmectl/references/modules/acme.md")
	if !strings.Contains(module, "Resolved SHA: `0000000000000000000000000000000000000000`") {
		t.Fatalf("module reference missing resolved SHA:\n%s", module)
	}
	if !strings.Contains(module, "`acmectl users list`") {
		t.Fatalf("module reference should use flat command path:\n%s", module)
	}
}

func TestRunCodegen_CommandPathFlatRewritesGeneratedExamples(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "overlays/acme.yaml", `commands:
  list:
    example: "acmectl acme users list -o json"
`)

	if err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-overlay", "overlays"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	generated := readCodegenFile(t, "internal/generated/acme/acme_gen.go")
	if strings.Contains(generated, `"acmectl acme users list -o json"`) {
		t.Fatalf("generated catalog example kept namespaced path:\n%s", generated)
	}
	if !strings.Contains(generated, `Example:     "acmectl users list -o json"`) {
		t.Fatalf("generated catalog example should use flat path:\n%s", generated)
	}
}

func TestRunCodegen_UsesSkillConfigFromManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
skill:
  root: agent-skills
  include: internal/skill-include
`)
	writeCodegenFile(t, "internal/skill-include/SKILL.md", "## Local guidance\n\nUse the team runbook.\n")

	if err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	skill := readCodegenFile(t, "agent-skills/acmectl/SKILL.md")
	if !strings.Contains(skill, "Use the team runbook.") {
		t.Fatalf("skill missing include guidance:\n%s", skill)
	}
	if _, err := os.Stat("skills"); !os.IsNotExist(err) {
		t.Fatalf("default skills directory should not be used, stat err = %v", err)
	}
}

func TestRunCodegen_SkillFlagsOverrideManifestConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
skill:
  root: yaml-skills
  include: internal/missing-yaml-include
`)
	writeCodegenFile(t, "internal/flag-include/SKILL.md", "## Flag guidance\n\nUse the flag include.\n")

	err := RunCodegen([]string{
		"-sources", "specs/sources.yaml",
		"-cache", ".cache",
		"-skill-root", "flag-skills",
		"-skill-include", "internal/flag-include",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	skill := readCodegenFile(t, "flag-skills/acmectl/SKILL.md")
	if !strings.Contains(skill, "Use the flag include.") {
		t.Fatalf("skill missing flag include guidance:\n%s", skill)
	}
	if _, err := os.Stat("yaml-skills"); !os.IsNotExist(err) {
		t.Fatalf("yaml skill root should not be used, stat err = %v", err)
	}
}

func TestRunCodegen_MissingSkillIncludeFailsBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
skill:
  include: internal/does-not-exist
`)

	err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing skill include error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
}

func TestRunCodegen_RejectsSkillIncludeInsideSkillRootBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "skills/include/SKILL.md", "blocked\n")

	err := RunCodegen([]string{
		"-sources", "specs/sources.yaml",
		"-cache", ".cache",
		"-skill-include", "skills/include",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected skill include overlap error")
	}
	if !strings.Contains(err.Error(), "must be outside skill root") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
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
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
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

	if err := RunBootstrap([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

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

	if err := RunBootstrap([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	localPath, err := filepath.Abs("api")
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(".cache/specs-sync/acme/sync-state.yaml")
	if err != nil {
		t.Fatalf("read sync state: %v", err)
	}
	if !strings.Contains(string(state), "source_kind: local") || !strings.Contains(string(state), "synced_from: "+localPath) {
		t.Fatalf("sync state = %s, want local source path %q", state, localPath)
	}
	for _, path := range []string{"internal/generated/acme/acme_gen.go", "skills/acmectl/SKILL.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), localPath) {
			t.Fatalf("%s encoded local path %q", path, localPath)
		}
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
	err = RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("codegen accepted stale local_path cache")
	}
	if !strings.Contains(err.Error(), "local_path") {
		t.Fatalf("error = %v, want local_path mismatch", err)
	}
}

func TestRunCodegen_SkillRootEmptyDisablesSkillGeneration(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, false)

	if err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-skill-root", ""}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat("skills"); !os.IsNotExist(err) {
		t.Fatalf("skills directory should not exist, stat err = %v", err)
	}
}

func TestRunCodegen_CommandPathNamespacedKeepsModuleSegment(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
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

	if err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-overlay", "overlays"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	modulesGen := readCodegenFile(t, "internal/generated/modules_gen.go")
	if strings.Contains(modulesGen, "MountFlat") {
		t.Fatalf("namespaced command_path should not flat mount:\n%s", modulesGen)
	}
	generated := readCodegenFile(t, "internal/generated/acme/acme_gen.go")
	if strings.Contains(generated, `"acmectl acme payment api list -o json"`) {
		t.Fatalf("generated catalog example kept unnormalized path:\n%s", generated)
	}
	if !strings.Contains(generated, `Example:     "acmectl acme payment list -o json"`) {
		t.Fatalf("generated catalog example should use namespaced Cobra path:\n%s", generated)
	}
	module := readCodegenFile(t, "skills/acmectl/references/modules/acme.md")
	if !strings.Contains(module, "`acmectl acme payment list`") {
		t.Fatalf("module reference should keep module path:\n%s", module)
	}
	if strings.Contains(module, "payment api list") {
		t.Fatalf("module reference kept unnormalized path:\n%s", module)
	}
}

func TestRunCodegen_CommandPathFlatRejectsRootConflict(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
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

	err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected flat conflict error, got %v", err)
	}
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

	err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "reserved root command") {
		t.Fatalf("expected reserved module name error, got %v", err)
	}
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
}

func TestRunCodegen_MissingManifestFailsWhenSkillEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, false)

	err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing manifest error")
	}
	if !strings.Contains(err.Error(), "cli.yaml") {
		t.Fatalf("error should mention cli.yaml, got %v", err)
	}
}

func TestRunCodegen_RejectsUnsafeSkillRootBeforeDeleting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: internal\n  short: Internal CLI\n")
	writeCodegenFile(t, "internal/sentinel.txt", "keep")

	err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-skill-root", "."}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsafe skill root error")
	}
	if !strings.Contains(err.Error(), "invalid skill root") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readCodegenFile(t, "internal/sentinel.txt"); got != "keep" {
		t.Fatalf("sentinel was changed: %q", got)
	}
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
}

func TestSkillOutputDirRejectsUnsafeRoots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, root := range []string{".", "..", string(filepath.Separator), "../skills"} {
		if _, err := skillOutputDir(root, "acmectl"); err == nil {
			t.Fatalf("expected %q to be rejected", root)
		}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readCodegenFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
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
