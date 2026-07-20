package lathecmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	kitup "github.com/lathe-cli/kitup/go"
	"github.com/lathe-cli/lathe/internal/latheskill"
)

type stringFlags []string

func (values *stringFlags) String() string { return fmt.Sprint([]string(*values)) }
func (values *stringFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func RunSkill(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(stderr, "Usage: lathe skill install [flags]")
		return flag.ErrHelp
	}
	if args[0] != "install" {
		return fmt.Errorf("unknown skill command %q", args[0])
	}
	fs := flag.NewFlagSet("lathe skill install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	scope := fs.String("scope", "", kitup.InstallUX.ScopeFlag)
	var agents stringFlags
	fs.Var(&agents, "agent", kitup.InstallUX.AgentFlag)
	yes := fs.Bool("yes", false, kitup.InstallUX.YesFlag)
	fs.BoolVar(yes, "y", false, kitup.InstallUX.YesFlag)
	dryRun := fs.Bool("dry-run", false, kitup.InstallUX.DryRunFlag)
	force := fs.Bool("force", false, kitup.InstallUX.ForceFlag)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("lathe skill install does not accept positional arguments")
	}
	scopeSet := false
	fs.Visit(func(value *flag.Flag) {
		if value.Name == "scope" {
			scopeSet = true
		}
	})
	parsed := kitup.ParseInstallFlags(kitup.InstallFlagValues{
		Scope:    *scope,
		ScopeSet: scopeSet,
		Agents:   agents,
		Yes:      *yes,
		DryRun:   *dryRun,
		Force:    *force,
	})
	if err := kitup.InstallFlagError(parsed.Errors); err != nil {
		return err
	}
	report, err := kitup.RunBundledSkillInstall(kitup.InstallWorkflowOptions{
		InstallOptions: kitup.InstallOptions{
			AppID:       "lathe",
			SkillBundle: kitup.FSBundle(latheskill.FS, latheskill.Root),
			Scope:       parsed.Scope,
			Agents:      parsed.Agents,
			Force:       parsed.Force,
		},
		Yes:          parsed.Yes,
		DryRun:       parsed.DryRun,
		DefaultScope: kitup.UserScope,
		ScopeSet:     parsed.ScopeSet,
		PromptScope:  true,
		In:           os.Stdin,
		Out:          stdout,
		Err:          stderr,
	})
	if err != nil {
		return err
	}
	return kitup.InstallWorkflowError(report)
}
