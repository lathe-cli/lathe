package lathecmd

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/codegen/app"
	"github.com/lathe-cli/lathe/internal/codegen/backends/graphql"
	"github.com/lathe-cli/lathe/internal/codegen/backends/openapi3"
	"github.com/lathe-cli/lathe/internal/codegen/backends/proto"
	"github.com/lathe-cli/lathe/internal/codegen/backends/swagger"
	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/specsync"
	"github.com/lathe-cli/lathe/pkg/config"
)

func runCodegen(sourcesPath string, manifestPath string, cacheRoot string, overlayDir string, skillFlags skillFlagOptions, output io.Writer) error {
	cfg, err := sourceconfig.Load(sourcesPath)
	if err != nil {
		return err
	}

	overlays, err := overlay.LoadDir(overlayDir)
	if err != nil {
		return err
	}

	absRoot, err := resolveCacheRoot(cacheRoot)
	if err != nil {
		return err
	}
	syncRoot := filepath.Join(absRoot, specsync.SyncSubdir)

	manifest, skillDir, skillInclude, err := resolveSkillOutput(manifestPath, skillFlags)
	if err != nil {
		return err
	}

	generated, err := buildGeneratedApp(cfg, overlays, syncRoot, manifest, skillDir, skillInclude)
	if err != nil {
		return err
	}
	if err := generated.Validate(); err != nil {
		return err
	}
	if err := generated.Write(); err != nil {
		return err
	}
	if generated.Manifest.Skill.Bundle {
		return pinSkillBundleDependencies(output)
	}
	return nil
}

func buildGeneratedApp(cfg *sourceconfig.Config, overlays map[string]overlay.Module, syncRoot string, manifest *config.Manifest, skillDir string, skillInclude render.SkillInclude) (*app.App, error) {
	generated := &app.App{Manifest: manifest}
	if skillDir != "" {
		generated.Skill = &app.Skill{Dir: skillDir, Include: skillInclude, Bundle: manifest.Skill.Bundle}
	}
	if manifest.Skill.Bundle && generated.Skill == nil {
		return nil, fmt.Errorf("skill.bundle requires skill generation")
	}

	ordered := cfg.Ordered()
	moduleNames := make([]string, 0, len(ordered))
	for _, src := range ordered {
		name := src.Name
		if src.DisplayName != "" {
			name = src.DisplayName
		}
		moduleNames = append(moduleNames, name)
	}
	var shortcutRootNames []string
	for i, src := range ordered {
		syncDir := filepath.Join(syncRoot, src.Name)
		if err := specsync.VerifyState(syncDir, src); err != nil {
			return nil, err
		}
		state, err := specsync.LoadState(syncDir)
		if err != nil {
			return nil, err
		}

		mod, err := parseSource(src, syncDir)
		if err != nil {
			return nil, err
		}

		specs := normalize.Normalize(mod)
		if src.DefaultHostname != nil {
			for i := range specs {
				specs[i].DefaultHostname = *src.DefaultHostname
			}
		}
		if err := render.ValidateOverlayModule(specs, overlays[src.Name]); err != nil {
			return nil, fmt.Errorf("source %q overlay: %w", src.Name, err)
		}
		specs, err = render.MergeOverlayModule(specs, overlays[src.Name])
		if err != nil {
			return nil, fmt.Errorf("source %q overlay: %w", src.Name, err)
		}
		if len(specs) == 0 {
			return nil, fmt.Errorf("source %q produced no commands: check its entry/file list, expose policy, and overlay ignore rules", src.Name)
		}
		cliName := moduleNames[i]
		flat, err := render.ResolveFlatCommandPath(manifest.CLI.CommandPath, len(ordered), specs)
		if err != nil {
			return nil, err
		}
		validateRootNames := append(append([]string(nil), moduleNames...), shortcutRootNames...)
		if err := render.ValidateShortcuts(validateRootNames, specs, flat); err != nil {
			return nil, err
		}
		for _, spec := range specs {
			for _, shortcut := range spec.Shortcuts {
				shortcutRootNames = append(shortcutRootNames, shortcut.Use)
			}
		}
		specs = render.RewriteCommandExamples(manifest.CLI.Name, cliName, specs, flat)
		generated.Modules = append(generated.Modules, app.Module{Source: src.Name, CLIName: cliName, Flat: flat, Specs: specs})
		if generated.Skill != nil {
			generated.Skill.Modules = append(generated.Skill.Modules, render.SkillModule{Source: src, State: state, Specs: specs})
		}
	}
	workflows, err := buildWorkflowSpecs(manifest, generated.Modules, shortcutRootNames)
	if err != nil {
		return nil, err
	}
	generated.Workflows = workflows
	return generated, nil
}

func parseSource(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	switch src.Backend {
	case sourceconfig.BackendSwagger:
		return swagger.Parse(src, syncDir)
	case sourceconfig.BackendProto:
		return proto.Parse(src, syncDir)
	case sourceconfig.BackendOpenAPI3:
		return openapi3.Parse(src, syncDir)
	case sourceconfig.BackendGraphQL:
		return graphql.Parse(src, syncDir)
	default:
		return nil, fmt.Errorf("source %q: unknown backend %q", src.Name, src.Backend)
	}
}
