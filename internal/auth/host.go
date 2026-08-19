package auth

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

// NewHostCommand is the top-level `host` command for the persisted default host.
func NewHostCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Manage configured hosts",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newHostDefault())
	return cmd
}

func newHostDefault() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "default",
		Short: "Show the persisted default host",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			hosts, err := config.LoadHosts()
			if err != nil {
				return err
			}
			if d := hosts.Default(); d != "" {
				fmt.Fprintln(cmd.OutOrStdout(), d)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "No default host is set")
			}
			return nil
		},
	}
	cmd.AddCommand(newHostDefaultSet(), newHostDefaultUnset())
	return cmd
}

func newHostDefaultSet() *cobra.Command {
	return &cobra.Command{
		Use:   "set <host>",
		Short: "Persist a default host among logged-in hosts",
		Args:  runtime.UsageArgs(cobra.ExactArgs(1)),
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
					return runtime.NewError(runtime.CodeUsage, runtime.ExitUsage,
						fmt.Sprintf("host %q is not logged in", hostname),
						fmt.Sprintf("run `%s auth login --hostname %s` or choose from: %s", config.Active().CLI.Name, hostname, strings.Join(names, ", ")),
						nil)
				}
				hosts.SetDefault(hostname)
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "✓ Default host set to %s\n", hostname)
			return nil
		},
	}
}

func newHostDefaultUnset() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Clear the persisted default host",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := config.MutateHosts(cmd.Context(), func(hosts *config.Hosts) error {
				hosts.ClearDefault()
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "✓ Default host unset")
			return nil
		},
	}
}
