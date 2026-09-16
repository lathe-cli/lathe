package auth

import (
	"fmt"
	"io"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"github.com/spf13/cobra"
)

func NewCommand(m *config.Manifest) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: fmt.Sprintf("Authenticate %s with a host", m.CLI.Name),
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newLogin(m), newStatus(), newLogout(), newUse())
	if len(m.Contexts) > 0 {
		cmd.AddCommand(newContextCommand(m))
	}
	return cmd
}

func NewHiddenLoginCommand(m *config.Manifest) *cobra.Command {
	cmd := newLogin(m)
	cmd.Hidden = true
	return cmd
}

func rootString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Root().PersistentFlags().GetString(name)
	return v
}

func rootBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Root().PersistentFlags().GetBool(name)
	return v
}

type authStatus struct {
	Hostname string           `json:"hostname,omitempty"`
	Source   string           `json:"source,omitempty"`
	Selected string           `json:"selected,omitempty"`
	Hosts    []authStatusHost `json:"hosts"`
}

type authStatusHost struct {
	Hostname string `json:"hostname"`
	User     string `json:"user,omitempty"`
	Auth     string `json:"auth"`
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "View authentication status",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateContextOutput(cmd); err != nil {
				return err
			}
			hostname := rootString(cmd, "hostname")
			hosts, err := config.LoadHosts()
			if err != nil {
				return err
			}
			names := hosts.Names()
			if len(names) == 0 {
				return runtime.NewNotAuthenticatedError()
			}
			if hostname != "" {
				if _, ok := hosts.Get(hostname); !ok {
					return runtime.NewNotAuthenticatedError()
				}
				names = []string{config.NormalizeHostname(hostname)}
			}
			res, _ := runtime.ResolveHostWithSource(cmd)
			selected := hosts.Selected()

			out := authStatus{
				Hostname: res.Hostname,
				Source:   res.Source,
				Selected: selected,
				Hosts:    make([]authStatusHost, 0, len(names)),
			}
			for _, n := range names {
				e, _ := hosts.Get(n)
				out.Hosts = append(out.Hosts, authStatusHost{Hostname: n, User: e.User, Auth: authTypeLabel(e)})
			}

			if format := rootString(cmd, "output"); format == "json" || format == "yaml" {
				return writeContextOutput(cmd, out, runtime.OutputHints{
					ListPath:       "hosts",
					DefaultColumns: []string{"hostname", "user", "auth"},
				})
			}
			w := cmd.OutOrStdout()
			for _, n := range names {
				e, _ := hosts.Get(n)
				printStatus(w, n, e, n == selected)
			}
			return nil
		},
	}
}

func newLogout() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove authentication for a host",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostname := rootString(cmd, "hostname")
			hosts, err := config.LoadHosts()
			if err != nil {
				return err
			}
			names := hosts.Names()
			if len(names) == 0 {
				return runtime.NewNotAuthenticatedError()
			}
			if hostname == "" {
				if len(names) == 1 {
					hostname = names[0]
				} else {
					return fmt.Errorf("multiple hosts configured, specify --hostname (have: %s)", strings.Join(names, ", "))
				}
			}
			if _, ok := hosts.Get(hostname); !ok {
				return runtime.NewNotAuthenticatedError()
			}
			if err := config.MutateHosts(cmd.Context(), func(hosts *config.Hosts) error {
				if !hosts.Delete(hostname) {
					return runtime.NewNotAuthenticatedError()
				}
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "✓ Logged out of %s\n", hostname)
			return nil
		},
	}
}

func authTypeLabel(e config.HostEntry) string {
	if e.AuthType == "" {
		return "bearer"
	}
	return e.AuthType
}

func printStatus(w io.Writer, hostname string, e config.HostEntry, selected bool) {
	user := e.User
	if user == "" {
		user = fmt.Sprintf("(unknown — run `%s auth login` to validate)", config.Active().CLI.Name)
	}
	marker := ""
	if selected {
		marker = " (selected)"
	}
	credential := maskedCredential(e)
	fmt.Fprintf(w, "%s%s\n  ✓ Logged in as %s\n  ✓ Auth: %s\n  ✓ Credential: %s\n", hostname, marker, user, authTypeLabel(e), credential)
	if e.LoginType != "" {
		loginLabel := e.LoginType
		if e.LoginProvider != "" {
			loginLabel += " (" + e.LoginProvider + ")"
		}
		fmt.Fprintf(w, "  ✓ Login: %s\n", loginLabel)
	}
}

func maskedCredential(e config.HostEntry) string {
	switch e.AuthType {
	case "apikey":
		return maskToken(e.APIKey)
	case "basic":
		return e.BasicUser + ":****"
	default:
		return maskToken(e.OAuthToken)
	}
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return "****"
	}
	return "****" + t[len(t)-4:]
}
