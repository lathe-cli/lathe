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
	if err := render.RenderModulesGen(mounts); err != nil {
		return err
	}
	if a.Skill == nil {
		return nil
	}
	return render.RenderSkillDirectoryWithInclude(a.Skill.Dir, a.Manifest, a.Skill.Modules, a.Skill.Include)
}
