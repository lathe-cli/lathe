// Package app models the generated CLI application that one codegen run is
// about to emit. Outputs are collected into an App first and written together,
// so a failing input never leaves partially generated output behind. The model
// is internal to codegen; it is not a stable extension API.
package app

import (
	"fmt"

	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

// App is the complete set of generated outputs for one codegen run.
type App struct {
	Manifest  *config.Manifest
	Modules   []Module
	Workflows []runtime.WorkflowSpec
	Skill     *Skill
}

// Module is one generated command module and how it mounts on the root command.
type Module struct {
	Source  string
	CLIName string
	Flat    bool
	Specs   []runtime.CommandSpec
}

// Skill is the optional generated Skill directory output.
type Skill struct {
	Dir     string
	Include render.SkillInclude
	Modules []render.SkillModule
	Bundle  bool
}

// Validate rejects app compositions that would produce a conflicting root
// command tree. Flat modules are skipped: their module name is never mounted,
// and flat root conflicts are rejected by ResolveFlatCommandPath.
func (a *App) Validate() error {
	names := make([]string, 0, len(a.Modules))
	for _, m := range a.Modules {
		if m.Flat {
			continue
		}
		names = append(names, m.CLIName)
	}
	for _, workflow := range a.Workflows {
		names = append(names, workflow.Use)
		names = append(names, workflow.Aliases...)
	}
	if err := render.ValidateModuleNames(names); err != nil {
		return err
	}
	for _, module := range a.Modules {
		for _, spec := range module.Specs {
			if err := validateCommandContexts(a.Manifest, spec); err != nil {
				return fmt.Errorf("command %q: %w", spec.Use, err)
			}
		}
	}
	for _, workflow := range a.Workflows {
		for _, step := range workflow.Steps {
			if err := validateCommandContexts(a.Manifest, step.Operation); err != nil {
				return fmt.Errorf("workflow %q step %q: %w", workflow.Use, step.ID, err)
			}
		}
	}
	return nil
}

func validateCommandContexts(manifest *config.Manifest, spec runtime.CommandSpec) error {
	contexts := map[string]config.ContextInfo(nil)
	if manifest != nil {
		contexts = manifest.Contexts
	}
	for _, param := range spec.Params {
		if param.Context == "" {
			continue
		}
		if _, ok := contexts[param.Context]; !ok {
			return fmt.Errorf("parameter %q references unknown context %q", param.Name, param.Context)
		}
		if param.GoType != "string" {
			return fmt.Errorf("context parameter %q must be a string", param.Name)
		}
	}
	if spec.SetContext == nil {
		return nil
	}
	if _, ok := contexts[spec.SetContext.Name]; !ok {
		return fmt.Errorf("sets unknown context %q", spec.SetContext.Name)
	}
	matches := 0
	for _, param := range spec.Params {
		if param.Name == spec.SetContext.Param || param.Flag == spec.SetContext.Param {
			matches++
			if param.GoType != "string" {
				return fmt.Errorf("context source parameter %q must be a string", spec.SetContext.Param)
			}
		}
	}
	if matches != 1 {
		return fmt.Errorf("context source parameter %q must match exactly one operation parameter", spec.SetContext.Param)
	}
	return nil
}

// Write renders every collected output.
func (a *App) Write() error {
	mounts := make([]render.ModuleMount, 0, len(a.Modules))
	for _, m := range a.Modules {
		if err := render.RenderModule(m.Source, m.CLIName, m.Specs, nil); err != nil {
			return err
		}
		mounts = append(mounts, render.ModuleMount{Name: m.Source, Flat: m.Flat})
	}
	opts := render.ModulesGenOptions{}
	if a.Skill != nil && a.Skill.Bundle {
		opts.SkillBundle = &render.SkillBundleMount{Root: render.SkillDirName(a.Manifest.CLI.Name)}
	}
	if len(a.Workflows) > 0 {
		opts.Workflows = true
		if err := render.RenderWorkflows(a.Workflows); err != nil {
			return err
		}
	} else if err := render.RemoveWorkflowsPackage(); err != nil {
		return err
	}
	if err := render.RenderModulesGenWithOptions(mounts, opts); err != nil {
		return err
	}
	if a.Skill == nil {
		return render.RemoveSkillBundlePackage()
	}
	if err := render.RenderSkillDirectoryWithInclude(a.Skill.Dir, a.Manifest, a.Skill.Modules, a.Skill.Include); err != nil {
		return err
	}
	if !a.Skill.Bundle {
		return render.RemoveSkillBundlePackage()
	}
	return render.RenderSkillBundlePackage(a.Skill.Dir, a.Manifest.CLI.Name)
}
