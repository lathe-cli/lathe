package lathecmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	case "version":
		fmt.Fprintf(stdout, "lathe %s (%s, %s)\n", lathe.Version, lathe.Commit, lathe.Date)
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
	return runCodegen(*sourcesPath, *manifestPath, *cacheRoot, *overlayDir, skillFlagsFrom(fs, skillRoot, skillInclude))
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
	return runCodegen(*sourcesPath, *manifestPath, absRoot, *overlayDir, skillFlagsFrom(fs, skillRoot, skillInclude))
}

func printRootUsage(output io.Writer) {
	fmt.Fprint(output, `Usage:
  lathe <command> [flags]

Commands:
  lathe specsync   Sync pinned upstream API specs into the local cache
  lathe codegen    Generate runtime command specs and optional Skill files
  lathe bootstrap  Sync specs and generate code in one pass
  lathe version    Print version information

Run "lathe <command> -h" for command-specific flags.
`)
}

func runCodegen(sourcesPath string, manifestPath string, cacheRoot string, overlayDir string, skillFlags skillFlagOptions) error {
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

	ordered := cfg.Ordered()
	var mounts []render.ModuleMount
	var skillModules []render.SkillModule
	for _, src := range ordered {
		syncDir := filepath.Join(syncRoot, src.Name)
		if err := specsync.VerifyState(syncDir, src.Name, src.Backend, src.PinnedTag); err != nil {
			return err
		}
		state, err := specsync.LoadState(syncDir)
		if err != nil {
			return err
		}

		mod, err := parseSource(src, syncDir)
		if err != nil {
			return err
		}

		specs := normalize.Normalize(mod)
		specs = render.MergeOverlayModule(specs, overlays[src.Name])
		if src.DefaultHostname != nil {
			for i := range specs {
				specs[i].DefaultHostname = *src.DefaultHostname
			}
		}
		cliName := src.Name
		if src.DisplayName != "" {
			cliName = src.DisplayName
		}
		flat, err := render.ResolveFlatCommandPath(manifest.CLI.CommandPath, len(ordered), specs, manifest.Auth.LoginAliases...)
		if err != nil {
			return err
		}
		if !flat {
			if alias := conflictingAuthLoginAlias(cliName, manifest.Auth.LoginAliases); alias != "" {
				return fmt.Errorf("auth.login_aliases command %q conflicts with source module %q", alias, cliName)
			}
		}
		specs = render.RewriteCommandExamples(manifest.CLI.Name, cliName, specs, flat)
		if err := render.RenderModule(src.Name, cliName, specs, nil); err != nil {
			return err
		}
		mounts = append(mounts, render.ModuleMount{Name: src.Name, Flat: flat})
		if skillDir != "" {
			skillModules = append(skillModules, render.SkillModule{Source: src, State: state, Specs: specs})
		}
	}
	if err := render.RenderModulesGen(mounts); err != nil {
		return err
	}
	if skillDir != "" {
		if err := render.RenderSkillDirectory(skillDir, manifest, skillModules); err != nil {
			return err
		}
		if err := render.ApplySkillIncludes(skillDir, skillInclude); err != nil {
			return err
		}
	}
	return nil
}

func conflictingAuthLoginAlias(moduleName string, aliases []string) string {
	moduleRoot := rootCommandName(moduleName)
	if moduleRoot == "" {
		return ""
	}
	for _, alias := range aliases {
		if rootCommandName(alias) == moduleRoot {
			return alias
		}
	}
	return ""
}

func rootCommandName(use string) string {
	fields := strings.Fields(strings.ToLower(use))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func resolveSkillOutput(manifestPath string, flags skillFlagOptions) (*config.Manifest, string, string, error) {
	manifest, rootConfig, include, err := loadCodegenManifest(manifestPath)
	if err != nil {
		if os.IsNotExist(err) && flags.RootSet && flags.Root == "" && (!flags.IncludeSet || flags.Include == "") {
			return &config.Manifest{CLI: config.CLIInfo{CommandPath: config.CommandPathAuto}}, "", "", nil
		}
		return nil, "", "", err
	}

	if flags.RootSet && flags.Root == "" {
		if flags.IncludeSet && flags.Include != "" {
			return nil, "", "", fmt.Errorf("skill include requires skill generation")
		}
		return manifest, "", "", nil
	}

	root := "skills"
	if rootConfig != nil {
		root = *rootConfig
	}
	if flags.RootSet {
		root = flags.Root
	}

	if flags.IncludeSet {
		include = flags.Include
	}

	if root == "" {
		if include != "" {
			return nil, "", "", fmt.Errorf("skill include requires skill generation")
		}
		return manifest, "", "", nil
	}

	skillDir, err := skillOutputDir(root, manifest.CLI.Name)
	if err != nil {
		return nil, "", "", err
	}
	if err := render.ValidateSkillIncludeRoot(root, include); err != nil {
		return nil, "", "", err
	}
	return manifest, skillDir, include, nil
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

func loadCodegenManifest(path string) (*config.Manifest, *string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	manifest, err := config.Load(data)
	if err != nil {
		return nil, nil, "", err
	}
	var codegen struct {
		Skill struct {
			Root    *string `yaml:"root"`
			Include string  `yaml:"include"`
		} `yaml:"skill"`
	}
	if err := yaml.Unmarshal(data, &codegen); err != nil {
		return nil, nil, "", fmt.Errorf("parse cli.yaml: %w", err)
	}
	return manifest, codegen.Skill.Root, codegen.Skill.Include, nil
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
