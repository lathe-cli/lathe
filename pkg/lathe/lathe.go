package lathe

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/auth"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const authGroupID = "auth"

// NewApp builds the root cobra command for a lathe-style CLI identified by m.
// Callers (typically a small main.go) are responsible for:
//   - calling config.Bind(m) before Execute() so package-level helpers
//     (hosts.configDir, runtime.ResolveHost) can reach the bound manifest;
//   - mounting generated module command trees onto the returned *cobra.Command;
//   - invoking .Execute() and mapping runtime.ErrNotAuthenticated to the
//     desired exit code.
func NewApp(m *config.Manifest) *cobra.Command {
	cmd := &cobra.Command{
		Use:          m.CLI.Name,
		Short:        m.CLI.Short,
		Long:         agentHint(m.CLI.Name, m.CLI.Short),
		SilenceUsage: true,
	}
	cmd.PersistentFlags().String("hostname", "", fmt.Sprintf("Server hostname (overrides $%s)", m.CLI.HostEnv))
	cmd.PersistentFlags().StringP("output", "o", "table", "Output format: table|json|yaml|raw")
	cmd.PersistentFlags().Bool("insecure", false, "Skip TLS certificate verification for this invocation")
	cmd.PersistentFlags().Bool("debug", false, "Print HTTP request/response details to stderr")

	cmd.AddGroup(
		&cobra.Group{ID: authGroupID, Title: "Authentication:"},
		&cobra.Group{ID: runtime.ModuleGroupID, Title: "Modules:"},
	)

	authCmd := auth.NewCommand(m)
	authCmd.GroupID = authGroupID
	cmd.AddCommand(authCmd)
	cmd.AddCommand(commandsCmd(m))
	cmd.AddCommand(searchCmd(m))
	cmd.AddCommand(versionCmd())
	return cmd
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
