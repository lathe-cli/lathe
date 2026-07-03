// Package app models the generated CLI application that one codegen run is
// about to emit. Outputs are collected into an App first and written together,
// so a failing input never leaves partially generated output behind. The model
// is internal to codegen; it is not a stable extension API.
package app

import (
	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

// App is the complete set of generated outputs for one codegen run.
type App struct {
	Manifest *config.Manifest
	Modules  []Module
	Skill    *Skill
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
	return render.ValidateModuleNames(names)
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
