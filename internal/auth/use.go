package auth

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func newUse() *cobra.Command {
	return &cobra.Command{
		Use:   "use <host>",
		Short: "Select the host later commands use by default",
		Long: "Select the host later commands use by default.\n\n" +
			"The selection is recorded against the logged-in host and is always overridden\n" +
			"by --hostname or the host environment variable. Logging out of the selected\n" +
			"host releases it.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return nil
			}
			return runtime.NewError(runtime.CodeUsage, runtime.ExitUsage,
				"host is required",
				fmt.Sprintf("run `%s <host>`", cmd.CommandPath()),
				nil)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			hostname := config.NormalizeHostname(args[0])
			if hostname == "" {
				return runtime.UsageError(cmd, fmt.Errorf("host is required"))
			}
			if err := config.MutateHosts(cmd.Context(), func(hosts *config.Hosts) error {
				if _, ok := hosts.Get(hostname); !ok {
					names := hosts.Names()
					if len(names) == 0 {
						return runtime.NewNotAuthenticatedError()
					}
					cli := config.Active().CLI.Name
					return runtime.NewError(runtime.CodeUsage, runtime.ExitUsage,
						fmt.Sprintf("host %q is not logged in", hostname),
						fmt.Sprintf("run `%s auth login --hostname %s`, or choose from: %s", cli, hostname, strings.Join(names, ", ")),
						nil)
				}
				hosts.Select(hostname)
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "✓ Now using %s\n", hostname)
			return nil
		},
	}
}
