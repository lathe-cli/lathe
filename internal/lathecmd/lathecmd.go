package lathecmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runCodegen(*sourcesPath, *manifestPath, *cacheRoot, *overlayDir, *skillRoot)
}

func RunBootstrap(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("lathe bootstrap", flag.ContinueOnError)
	fs.SetOutput(output)
	sourcesPath := fs.String("sources", "specs/sources.yaml", "sources.yaml path")
	manifestPath := fs.String("manifest", "cli.yaml", "cli.yaml path")
	cacheRoot := fs.String("cache", "", "cache root (default $LATHE_SPECS_CACHE or .cache)")
	overlayDir := fs.String("overlay", "", "directory containing <module>.yaml overlay files (optional)")
	skillRoot := fs.String("skill-root", "skills", "skill output root, or empty to disable skill generation")
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
	return runCodegen(*sourcesPath, *manifestPath, absRoot, *overlayDir, *skillRoot)
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

func runCodegen(sourcesPath string, manifestPath string, cacheRoot string, overlayDir string, skillRoot string) error {
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

	var manifest *config.Manifest
	var skillDir string
	if skillRoot != "" {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return err
		}
		manifest, err = config.Load(data)
		if err != nil {
			return err
		}
		skillDir, err = skillOutputDir(skillRoot, manifest.CLI.Name)
		if err != nil {
			return err
		}
	}

	var names []string
	var skillModules []render.SkillModule
	for _, src := range cfg.Ordered() {
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
		specs = render.MergeOverlay(specs, overlays[src.Name])
		cliName := src.Name
		if src.DisplayName != "" {
			cliName = src.DisplayName
		}
		if err := render.RenderModule(src.Name, cliName, specs, nil); err != nil {
			return err
		}
		names = append(names, src.Name)
		if manifest != nil {
			skillModules = append(skillModules, render.SkillModule{Source: src, State: state, Specs: specs})
		}
	}
	if err := render.RenderModulesGen(names); err != nil {
		return err
	}
	if manifest != nil {
		if err := render.RenderSkillDirectory(skillDir, manifest, skillModules); err != nil {
			return err
		}
	}
	return nil
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
	default:
		return nil, fmt.Errorf("source %q: unknown backend %q", src.Name, src.Backend)
	}
}

func skillOutputDir(root string, cliName string) (string, error) {
	clean := filepath.Clean(root)
	if root == "" || clean == "." || clean == ".." || clean == string(filepath.Separator) || hasParentTraversal(clean) {
		return "", fmt.Errorf("invalid skill root %q", root)
	}
	absRoot, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if absRoot == absCWD {
		return "", fmt.Errorf("invalid skill root %q: must not be the current project root", root)
	}
	return filepath.Join(clean, render.SkillDirName(cliName)), nil
}

func hasParentTraversal(path string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == ".." {
			return true
		}
	}
	return false
}
