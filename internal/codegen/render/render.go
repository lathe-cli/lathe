package render

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const (
	GeneratedRoot  = "internal/generated"
	ModulesGenFile = "internal/generated/modules_gen.go"
	SkillBundleDir = "internal/generated/skillbundle"
	WorkflowsDir   = "internal/generated/workflows"
)

type moduleCtx struct {
	Module        string
	CLIName       string
	RuntimePkg    string
	SchemaVersion int
	Ops           []runtime.CommandSpec
}

type ModuleMount struct {
	Name string
	Flat bool
}

type ModulesGenOptions struct {
	SkillBundle *SkillBundleMount
	Workflows   bool
}

type SkillBundleMount struct {
	Root string
}

type workflowCtx struct {
	RuntimePkg string
	Specs      []runtime.WorkflowSpec
}

const RuntimePkg = "github.com/lathe-cli/lathe/pkg/runtime"

func RenderModule(name, cliName string, specs []runtime.CommandSpec, overrides map[string]overlay.Override) error {
	if cliName == "" {
		cliName = name
	}
	mod := overlay.Module{Commands: overrides}
	if err := ValidateOverlayModule(specs, mod); err != nil {
		return err
	}
	merged, err := MergeOverlayModule(specs, mod)
	if err != nil {
		return err
	}
	return renderModuleSpecs(name, cliName, merged)
}

func renderModuleSpecs(name, cliName string, specs []runtime.CommandSpec) error {
	outPath := filepath.Join(GeneratedRoot, name, name+"_gen.go")
	ctx := moduleCtx{Module: name, CLIName: cliName, RuntimePkg: RuntimePkg, SchemaVersion: runtime.SchemaVersion, Ops: specs}
	if err := writeGoFile(outPath, moduleTmpl, ctx); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d commands\n", outPath, len(specs))
	return nil
}

func writeGoFile(outPath string, tmpl *template.Template, data any) error {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(outPath+".unformatted", []byte(buf.String()), 0o644)
		return err
	}
	return os.WriteFile(outPath, formatted, 0o644)
}

func RenderModulesGen(modules []ModuleMount) error {
	return RenderModulesGenWithOptions(modules, ModulesGenOptions{})
}

func RenderModulesGenWithOptions(modules []ModuleMount, opts ModulesGenOptions) error {
	mp, err := modulePath()
	if err != nil {
		return err
	}
	if err := writeGoFile(ModulesGenFile, modulesTmpl, struct {
		Prefix      string
		Modules     []ModuleMount
		SkillBundle *SkillBundleMount
		Workflows   bool
	}{mp + "/internal/generated/", modules, opts.SkillBundle, opts.Workflows}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d modules\n", ModulesGenFile, len(modules))
	return nil
}

func RenderWorkflows(specs []runtime.WorkflowSpec) error {
	outPath := filepath.Join(WorkflowsDir, "workflows_gen.go")
	if err := writeGoFile(outPath, workflowsTmpl, workflowCtx{RuntimePkg: RuntimePkg, Specs: specs}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d workflows\n", outPath, len(specs))
	return nil
}

func RemoveWorkflowsPackage() error {
	return os.RemoveAll(WorkflowsDir)
}

func RenderSkillBundlePackage(skillDir string, cliName string) error {
	root := SkillDirName(cliName)
	dst := filepath.Join(SkillBundleDir, root)
	if err := os.MkdirAll(SkillBundleDir, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := copySkillBundleFiles(skillDir, dst); err != nil {
		return err
	}
	if err := writeGoFile(filepath.Join(SkillBundleDir, "skillbundle_gen.go"), skillBundleTmpl, struct{ Root string }{root}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", SkillBundleDir)
	return nil
}

func RemoveSkillBundlePackage() error {
	return os.RemoveAll(SkillBundleDir)
}

func copySkillBundleFiles(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module directive in go.mod")
}
