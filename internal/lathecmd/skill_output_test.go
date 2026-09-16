package lathecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestRun_CodegenSubcommandGeneratesSkillDirectoryByDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)

	testutil.NoError(t, Run([]string{"codegen", "-sources", "specs/sources.yaml", "-cache", ".cache"}))

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
	testutil.Require(t, strings.Contains(skill, "acmectl search \"<intent>\" --json"), "skill missing search workflow:\n%s", skill)
	modulesGen := readCodegenFile(t, "internal/generated/modules_gen.go")
	testutil.Require(t, strings.Contains(modulesGen, "acme.MountFlat(root)"), "single-module default should flat mount:\n%s", modulesGen)
	module := readCodegenFile(t, "skills/acmectl/references/modules/acme.md")
	testutil.Require(t, strings.Contains(module, "Resolved SHA: `0000000000000000000000000000000000000000`"), "module reference missing resolved SHA:\n%s", module)
	testutil.Require(t, strings.Contains(module, "`acmectl users list`"), "module reference should use flat command path:\n%s", module)
}

func TestRunCodegen_UsesSkillConfigFromManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
skill:
  root: agent-skills
  include: internal/skill-include
`)
	writeCodegenFile(t, "internal/skill-include/SKILL.md", "## Local guidance\n\nUse the team runbook.\n")

	testutil.NoError(t, runTestCodegen())

	skill := readCodegenFile(t, "agent-skills/acmectl/SKILL.md")
	testutil.Require(t, strings.Contains(skill, "Use the team runbook."), "skill missing include guidance:\n%s", skill)
	if _, err := os.Stat("skills"); !os.IsNotExist(err) {
		t.Fatalf("default skills directory should not be used, stat err = %v", err)
	}
}

func TestRunCodegen_SkillBundleGeneratesEmbedAndPinsDeps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
skill:
  bundle: true
`)
	logPath := filepath.Join(dir, "go-args.txt")
	bin := filepath.Join(dir, "bin")
	testutil.NoError(t, os.MkdirAll(bin, 0o755))
	goScript := filepath.Join(bin, "go")
	testutil.NoError(t, os.WriteFile(goScript, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > "+strconv.Quote(logPath)+"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var out bytes.Buffer
	testutil.NoError(t, RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache"}, &out))

	for _, path := range []string{
		"internal/generated/skillbundle/skillbundle_gen.go",
		"internal/generated/skillbundle/acmectl/SKILL.md",
		"internal/generated/skillbundle/acmectl/agents/openai.yaml",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	if _, err := os.Stat("internal/generated/skillbundle/acmectl/.lathe-skill"); !os.IsNotExist(err) {
		t.Fatalf("bundle should not include skill owner file, stat err = %v", err)
	}
	modulesGen := readCodegenFile(t, "internal/generated/modules_gen.go")
	for _, want := range []string{
		`func Mount(root *cobra.Command) error`,
		`lathekitup.FSBundle(lathegeneratedskillbundle.FS, lathegeneratedskillbundle.Root)`,
		`latheruntime.AttachCapability(root, latheruntime.CapabilitySkillBundle)`,
		`lathekitupcobra.NewSkillCommand`,
	} {
		testutil.Require(t, strings.Contains(modulesGen, want), "modules_gen.go missing %q:\n%s", want, modulesGen)
	}
	args := readCodegenFile(t, logPath)
	wantArgs := "get " + kitupGoDependency + " " + kitupGoCobraDependency + "\n"
	testutil.Require(t, args == wantArgs, "go args = %q, want %q", args, wantArgs)
	if !strings.Contains(out.String(), "go "+strings.TrimSpace(wantArgs)) {
		t.Fatalf("output missing go get command:\n%s", out.String())
	}
}

func TestRunCodegen_SkillFlagsOverrideManifestConfig(t *testing.T) {
	t.Chdir(t.TempDir())
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
	testutil.Require(t, err == nil, "run: %v", err)

	skill := readCodegenFile(t, "flag-skills/acmectl/SKILL.md")
	testutil.Require(t, strings.Contains(skill, "Use the flag include."), "skill missing flag include guidance:\n%s", skill)
	if _, err := os.Stat("yaml-skills"); !os.IsNotExist(err) {
		t.Fatalf("yaml skill root should not be used, stat err = %v", err)
	}
}

func TestRunCodegen_MissingSkillIncludeFailsBeforeWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", `cli:
  name: acmectl
  short: Acme CLI
skill:
  include: internal/does-not-exist
`)

	err := runTestCodegen()
	testutil.Require(t, err != nil, "expected missing skill include error")
	testutil.Require(t, strings.Contains(err.Error(), "does not exist"), "unexpected error: %v", err)
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
}

func TestRunCodegen_RejectsSkillIncludeInsideSkillRootBeforeWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "skills/include/SKILL.md", "blocked\n")

	err := RunCodegen([]string{
		"-sources", "specs/sources.yaml",
		"-cache", ".cache",
		"-skill-include", "skills/include",
	}, &bytes.Buffer{})
	testutil.Require(t, err != nil, "expected skill include overlap error")
	testutil.Require(t, strings.Contains(err.Error(), "must be outside skill root"), "unexpected error: %v", err)
	if _, err := os.Stat("internal/generated"); !os.IsNotExist(err) {
		t.Fatalf("codegen should fail before writing generated code, stat err = %v", err)
	}
}

func TestRunCodegen_SkillRootEmptyDisablesSkillGeneration(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, false)

	testutil.NoError(t, RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-skill-root", ""}, &bytes.Buffer{}))
	if _, err := os.Stat("skills"); !os.IsNotExist(err) {
		t.Fatalf("skills directory should not exist, stat err = %v", err)
	}
}

func TestRunCodegen_MissingManifestFailsWhenSkillEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	seedCodegenProject(t, false)

	err := runTestCodegen()
	testutil.Require(t, err != nil, "expected missing manifest error")
	testutil.Require(t, strings.Contains(err.Error(), "cli.yaml"), "error should mention cli.yaml, got %v", err)
}

func TestRunCodegen_RejectsUnsafeSkillRootBeforeDeleting(t *testing.T) {
	t.Chdir(t.TempDir())
	seedCodegenProject(t, true)
	writeCodegenFile(t, "cli.yaml", "cli:\n  name: internal\n  short: Internal CLI\n")
	writeCodegenFile(t, "internal/sentinel.txt", "keep")

	err := RunCodegen([]string{"-sources", "specs/sources.yaml", "-cache", ".cache", "-skill-root", "."}, &bytes.Buffer{})
	testutil.Require(t, err != nil, "expected unsafe skill root error")
	testutil.Require(t, strings.Contains(err.Error(), "invalid skill root"), "unexpected error: %v", err)
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
