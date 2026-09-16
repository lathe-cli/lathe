package lathecmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/specsync"
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
	return runGeneration(args, output, false)
}

func RunBootstrap(args []string, output io.Writer) error {
	return runGeneration(args, output, true)
}

func runGeneration(args []string, output io.Writer, bootstrap bool) error {
	name := "lathe codegen"
	if bootstrap {
		name = "lathe bootstrap"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
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
	if bootstrap {
		cfg, err := sourceconfig.Load(*sourcesPath)
		if err != nil {
			return err
		}
		*cacheRoot, err = resolveCacheRoot(*cacheRoot)
		if err != nil {
			return err
		}
		if err := specsync.Sync(cfg, specsync.Options{CacheRoot: *cacheRoot}); err != nil {
			return err
		}
	}
	return runCodegen(*sourcesPath, *manifestPath, *cacheRoot, *overlayDir, skillFlagsFrom(fs, skillRoot, skillInclude), output)
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

func resolveCacheRoot(root string) (string, error) {
	if root == "" {
		root = os.Getenv("LATHE_SPECS_CACHE")
	}
	if root == "" {
		root = ".cache"
	}
	return filepath.Abs(root)
}
