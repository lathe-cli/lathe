package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/pkg/config"
)

func TestValidateRejectsSkillIncludeBeforeWriting(t *testing.T) {
	for _, include := range []render.SkillInclude{
		{Path: "missing"},
		{Files: map[string]render.SkillFileAction{"../escape": render.SkillFileCreate}},
		{Files: map[string]render.SkillFileAction{"SKILL.md": render.SkillFileReplace}},
	} {
		t.Run(include.Path, func(t *testing.T) {
			t.Chdir(t.TempDir())
			a := &App{Manifest: &config.Manifest{CLI: config.CLIInfo{Name: "demo"}}, Modules: []Module{{Source: "api", CLIName: "api"}}, Skill: &Skill{Dir: "skills/demo", Include: include}}
			if err := a.Validate(); err == nil {
				t.Fatal("invalid skill include accepted")
			}
			if err := a.Write(); err == nil {
				t.Fatal("write accepted invalid skill include")
			}
			if _, err := os.Stat(filepath.Join("internal", "generated")); !os.IsNotExist(err) {
				t.Fatalf("Go output changed before include validation: %v", err)
			}
		})
	}
}
