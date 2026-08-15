package lathe

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/auth"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const (
	authGroupID     = "auth"
	metaCommandName = "__lathe"
)

// NewApp builds the root cobra command for a lathe-style CLI identified by m.
// It binds m for package-level helpers before returning the command.
// Callers that need the standard generated-CLI entrypoint should use Run.
// Advanced callers may still mount generated module command trees directly
// onto the returned *cobra.Command and execute it themselves.
func NewApp(m *config.Manifest) *cobra.Command {
	config.Bind(m)

	cmd := &cobra.Command{
		Use:     m.CLI.Name,
		Short:   m.CLI.Short,
		Long:    agentHint(m.CLI.Name, m.CLI.Short),
		Version: versionInfo(),
		Args:    runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			if !slices.Contains(runtime.FormatterNames(), format) {
				return runtime.UsageError(cmd, fmt.Errorf("unsupported output format"))
			}
			return nil
		},
		SilenceUsage: true,
	}
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return runtime.UsageError(cmd, err)
	})
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.SetVersionTemplate("{{.Use}} {{.Version}}\n")
	cmd.PersistentFlags().String("hostname", "", fmt.Sprintf("Server hostname (overrides $%s)", m.CLI.HostEnv))
	cmd.PersistentFlags().StringP("output", "o", "table", "Output format: table|json|yaml|raw")
	cmd.PersistentFlags().Bool("insecure", false, "Skip TLS certificate verification for this invocation")
	cmd.PersistentFlags().Bool("debug", false, "Print HTTP request/response details to stderr")
	_ = cmd.PersistentFlags().MarkHidden("insecure")
	_ = cmd.PersistentFlags().MarkHidden("debug")

	cmd.AddGroup(
		&cobra.Group{ID: authGroupID, Title: "Authentication:"},
		&cobra.Group{ID: runtime.ModuleGroupID, Title: "Modules:"},
	)

	authCmd := auth.NewCommand(m)
	authCmd.GroupID = authGroupID
	cmd.AddCommand(authCmd)
	cmd.AddCommand(auth.NewHiddenLoginCommand(m))
	cmd.AddCommand(commandsCmd(m))
	cmd.AddCommand(searchCmd(m))
	if m.Update.GitHub != nil {
		cmd.AddCommand(updateCmd(m))
	}
	cmd.AddCommand(metaCmd(m))
	return cmd
}

func metaCmd(m *config.Manifest) *cobra.Command {
	cmd := &cobra.Command{
		Use:    metaCommandName,
		Short:  "Lathe control commands",
		Hidden: true,
		Args:   runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(versionCmd())
	cmd.AddCommand(verifyCmd(m))
	cmd.InitDefaultCompletionCmd()
	rewriteCompletionHelp(cmd, m.CLI.Name)
	return cmd
}

func rewriteCompletionHelp(cmd *cobra.Command, cliName string) {
	oldPath := metaCommandName + " completion"
	newPath := cliName + " " + metaCommandName + " completion"
	for _, child := range cmd.Commands() {
		child.Long = strings.ReplaceAll(child.Long, oldPath, newPath)
		child.Long = strings.ReplaceAll(child.Long, "for "+metaCommandName, "for "+cliName)
		rewriteCompletionHelp(child, cliName)
	}
}

// agentHint surfaces the catalog protocol in `<cli> --help` so agents
// discover it without any preinstalled SKILL file.
func agentHint(name, short string) string {
	if short == "" {
		short = name
	}
	return fmt.Sprintf(`%s

For agents:
  %s commands --json           machine-readable command catalog
  %s commands show <path...>   precise spec for one command
  %s search "<intent>"         find commands by keyword
`, short, name, name, name)
}
