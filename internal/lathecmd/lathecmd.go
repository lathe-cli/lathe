package lathecmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	"github.com/lathe-cli/lathe/pkg/lathe"
	"gopkg.in/yaml.v3"
)

type skillFlagOptions struct {
	Root       string
	RootSet    bool
	Include    string
	IncludeSet bool
}

const (
	kitupGoDependency      = "github.com/lathe-cli/kitup/go@v0.1.3"
	kitupGoCobraDependency = "github.com/lathe-cli/kitup/go-cobra@v0.1.3"
)

func Run(args []string) error {
	return runWithOutputs(args, os.Stdout, os.Stderr)
}

func RunWithOutput(args []string, output io.Writer) error {
	return runWithOutputs(args, output, output)
}

func runWithOutputs(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return flag.ErrHelp
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(stderr)
		return flag.ErrHelp
	case "specsync":
		return RunSpecsync(args[1:], stderr)
	case "codegen":
		return RunCodegen(args[1:], stderr)
	case "bootstrap":
		return RunBootstrap(args[1:], stderr)
	case "init":
		return RunInit(args[1:], stdout, stderr)
	case "skill":
		return RunSkill(args[1:], stdout, stderr)
	case "version":
		version, commit, date := lathe.VersionInfo()
		fmt.Fprintf(stdout, "lathe %s (%s, %s)\n", version, commit, date)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func RunSpecsync(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("lathe specsync", flag.ContinueOnError)
	fs.SetOutput(output)
	sourcesPath := fs.String("sources", "specs/sources.yaml", "sources.yaml path")
	cacheRoot := fs.String("cache", "", "cache root (default $LATHE_SPECS_CACHE or .cache)")
	filter := fs.String("source", "", "sync only this source")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := sourceconfig.Load(*sourcesPath)
	if err != nil {
		return err
	}
	absRoot, err := resolveCacheRoot(*cacheRoot)
	if err != nil {
		return err
	}
	return specsync.Sync(cfg, specsync.Options{
		CacheRoot: absRoot,
		Filter:    *filter,
	})
}

func RunCodegen(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("lathe codegen", flag.ContinueOnError)
	fs.SetOutput(output)
	sourcesPath := fs.String("sources", "specs/sources.yaml", "sources.yaml path")
	manifestPath := fs.String("manifest", "cli.yaml", "cli.yaml path")
	cacheRoot := fs.String("cache", "", "cache root (default $LATHE_SPECS_CACHE or .cache)")
	overlayDir := fs.String("overlay", "", "directory containing <module>.yaml overlay files (optional)")
	skillRoot := fs.String("skill-root", "skills", "skill output root, or empty to disable skill generation")
	skillInclude := fs.String("skill-include", "", "directory of Skill resources merged into generated skill files (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runCodegen(*sourcesPath, *manifestPath, *cacheRoot, *overlayDir, skillFlagsFrom(fs, skillRoot, skillInclude), output)
}

func RunBootstrap(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("lathe bootstrap", flag.ContinueOnError)
	fs.SetOutput(output)
	sourcesPath := fs.String("sources", "specs/sources.yaml", "sources.yaml path")
	manifestPath := fs.String("manifest", "cli.yaml", "cli.yaml path")
	cacheRoot := fs.String("cache", "", "cache root (default $LATHE_SPECS_CACHE or .cache)")
	overlayDir := fs.String("overlay", "", "directory containing <module>.yaml overlay files (optional)")
	skillRoot := fs.String("skill-root", "skills", "skill output root, or empty to disable skill generation")
	skillInclude := fs.String("skill-include", "", "directory of Skill resources merged into generated skill files (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := sourceconfig.Load(*sourcesPath)
	if err != nil {
		return err
	}
	absRoot, err := resolveCacheRoot(*cacheRoot)
	if err != nil {
		return err
	}
	if err := specsync.Sync(cfg, specsync.Options{CacheRoot: absRoot}); err != nil {
		return err
	}
	return runCodegen(*sourcesPath, *manifestPath, absRoot, *overlayDir, skillFlagsFrom(fs, skillRoot, skillInclude), output)
}

func printRootUsage(output io.Writer) {
	fmt.Fprint(output, `Usage:
  lathe <command> [flags]

Commands:
  lathe init        Create a CLI-first application repository
  lathe skill       Manage the bundled Lathe Agent Skill
  lathe specsync   Sync pinned upstream API specs into the local cache
  lathe codegen    Generate runtime command specs and optional Skill files
  lathe bootstrap  Sync specs and generate code in one pass
  lathe version    Print version information

Run "lathe <command> -h" for command-specific flags.
`)
}

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

// buildGeneratedApp parses and normalizes every configured source into the
// generated app model without writing any output.
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
		if err := render.ValidateOverlayModule(specs, overlays[src.Name]); err != nil {
			return nil, fmt.Errorf("source %q overlay: %w", src.Name, err)
		}
		specs = render.MergeOverlayModule(specs, overlays[src.Name])
		if len(specs) == 0 {
			return nil, fmt.Errorf("source %q produced no commands: check its entry/file list, expose policy, and overlay ignore rules", src.Name)
		}
		if src.DefaultHostname != nil {
			for i := range specs {
				specs[i].DefaultHostname = *src.DefaultHostname
			}
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

func resolveSkillOutput(manifestPath string, flags skillFlagOptions) (*config.Manifest, string, render.SkillInclude, error) {
	manifest, rootConfig, include, err := loadCodegenManifest(manifestPath)
	if err != nil {
		if os.IsNotExist(err) && flags.RootSet && flags.Root == "" && (!flags.IncludeSet || flags.Include == "") {
			return &config.Manifest{CLI: config.CLIInfo{CommandPath: config.CommandPathAuto}}, "", render.SkillInclude{}, nil
		}
		return nil, "", render.SkillInclude{}, err
	}

	if flags.RootSet && flags.Root == "" {
		if manifest.Skill.Bundle {
			return nil, "", render.SkillInclude{}, fmt.Errorf("skill.bundle requires skill generation")
		}
		if skillIncludeConfigured(include) || flags.IncludeSet && flags.Include != "" {
			return nil, "", render.SkillInclude{}, fmt.Errorf("skill include requires skill generation")
		}
		return manifest, "", render.SkillInclude{}, nil
	}

	root := "skills"
	if rootConfig != nil {
		root = *rootConfig
	}
	if flags.RootSet {
		root = flags.Root
	}

	if flags.IncludeSet {
		include.Path = flags.Include
	}

	if root == "" {
		if manifest.Skill.Bundle {
			return nil, "", render.SkillInclude{}, fmt.Errorf("skill.bundle requires skill generation")
		}
		if skillIncludeConfigured(include) {
			return nil, "", render.SkillInclude{}, fmt.Errorf("skill include requires skill generation")
		}
		return manifest, "", render.SkillInclude{}, nil
	}

	skillDir, err := skillOutputDir(root, manifest.CLI.Name)
	if err != nil {
		return nil, "", render.SkillInclude{}, err
	}
	if err := render.ValidateSkillIncludeRoot(root, include.Path); err != nil {
		return nil, "", render.SkillInclude{}, err
	}
	return manifest, skillDir, include, nil
}

func pinSkillBundleDependencies(output io.Writer) error {
	args := []string{"get", kitupGoDependency, kitupGoCobraDependency}
	fmt.Fprintf(output, "go %s\n", strings.Join(args, " "))
	cmd := exec.Command("go", args...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pin skill bundle dependencies: %w", err)
	}
	return nil
}

func skillIncludeConfigured(include render.SkillInclude) bool {
	return include.Path != "" || len(include.Files) > 0
}

func skillFlagsFrom(fs *flag.FlagSet, root, include *string) skillFlagOptions {
	var rootSet, includeSet bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "skill-root":
			rootSet = true
		case "skill-include":
			includeSet = true
		}
	})
	return skillFlagOptions{
		Root:       *root,
		RootSet:    rootSet,
		Include:    *include,
		IncludeSet: includeSet,
	}
}

func loadCodegenManifest(path string) (*config.Manifest, *string, render.SkillInclude, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, render.SkillInclude{}, err
	}
	manifest, err := config.Load(data)
	if err != nil {
		return nil, nil, render.SkillInclude{}, err
	}
	var codegen struct {
		Skill struct {
			Root    *string            `yaml:"root"`
			Include skillIncludeConfig `yaml:"include"`
		} `yaml:"skill"`
	}
	if err := yaml.Unmarshal(data, &codegen); err != nil {
		return nil, nil, render.SkillInclude{}, fmt.Errorf("parse cli.yaml: %w", err)
	}
	return manifest, codegen.Skill.Root, codegen.Skill.Include.SkillInclude, nil
}

type skillIncludeConfig struct {
	render.SkillInclude
}

func (c *skillIncludeConfig) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		c.Path = value.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i < len(value.Content); i += 2 {
			key := value.Content[i]
			val := value.Content[i+1]
			switch key.Value {
			case "path":
				if val.Kind != yaml.ScalarNode {
					return fmt.Errorf("skill.include.path must be a string")
				}
				c.Path = val.Value
			case "files":
				files, err := decodeSkillIncludeFiles(val)
				if err != nil {
					return err
				}
				c.Files = files
			default:
				return fmt.Errorf("unknown skill.include field %q", key.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("skill.include must be a string or mapping")
	}
}

func decodeSkillIncludeFiles(node *yaml.Node) (map[string]render.SkillFileAction, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("skill.include.files must be a mapping")
	}
	files := map[string]render.SkillFileAction{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		if key.Kind != yaml.ScalarNode || val.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("skill.include.files entries must map file paths to actions")
		}
		if _, ok := files[key.Value]; ok {
			return nil, fmt.Errorf("duplicate skill.include.files entry %q", key.Value)
		}
		files[key.Value] = render.SkillFileAction(val.Value)
	}
	return files, nil
}

func resolveCacheRoot(root string) (string, error) {
	if root == "" {
		root = os.Getenv("LATHE_SPECS_CACHE")
	}
	if root == "" {
		root = ".cache"
	}
	return filepath.Abs(root)
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

func skillOutputDir(root string, cliName string) (string, error) {
	clean := filepath.Clean(root)
	if clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("invalid skill root %q", root)
	}
	return filepath.Join(clean, render.SkillDirName(cliName)), nil
}
