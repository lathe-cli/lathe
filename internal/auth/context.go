package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"github.com/spf13/cobra"
)

type contextStatus struct {
	Hostname string               `json:"hostname"`
	Contexts []contextStatusEntry `json:"contexts"`
}

type contextStatusEntry struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Source   string `json:"source"`
	Env      string `json:"env,omitempty"`
	LocalSet bool   `json:"local_set"`
}

func newContextCommand(m *config.Manifest) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage account-scoped command defaults",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newContextStatus(m), newContextUnset(m))
	for _, info := range m.Contexts {
		if info.LocalSet {
			cmd.AddCommand(newContextSet(m))
			break
		}
	}
	return cmd
}

func newContextStatus(m *config.Manifest) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "View active context values",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateContextOutput(cmd); err != nil {
				return err
			}
			hostname, entry, err := selectedHostEntry(cmd, false)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(m.Contexts))
			for name := range m.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)
			out := contextStatus{Hostname: hostname, Contexts: make([]contextStatusEntry, 0, len(names))}
			for _, name := range names {
				info := m.Contexts[name]
				item := contextStatusEntry{Name: name, Env: info.Env, LocalSet: info.LocalSet, Source: "unset"}
				if info.Env != "" {
					if value := strings.TrimSpace(os.Getenv(info.Env)); value != "" {
						item.Value, item.Source = value, "env"
					}
				}
				if item.Value == "" {
					if value := strings.TrimSpace(entry.Contexts[name]); value != "" {
						item.Value, item.Source = value, "stored"
					}
				}
				out.Contexts = append(out.Contexts, item)
			}
			return writeContextOutput(cmd, out, runtime.OutputHints{ListPath: "contexts", DefaultColumns: []string{"name", "value", "source", "env", "local_set"}})
		},
	}
}

func newContextSet(m *config.Manifest) *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> <value>",
		Short: "Set a locally managed context value",
		Args:  runtime.UsageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateContextOutput(cmd); err != nil {
				return err
			}
			name, value := args[0], strings.TrimSpace(args[1])
			info, ok := m.Contexts[name]
			if !ok {
				return runtime.UsageError(cmd, fmt.Errorf("unknown context %q", name))
			}
			if !info.LocalSet {
				return runtime.NewError(runtime.CodeUsage, runtime.ExitUsage, "context is server-managed", "use the generated selector operation or pass the bound flag explicitly", fmt.Errorf("context %q does not allow local set", name))
			}
			if value == "" {
				return runtime.UsageError(cmd, errors.New("context value must not be empty"))
			}
			hostname, _, err := selectedHostEntry(cmd, true)
			if err != nil {
				return err
			}
			if err := mutateHostContext(cmd, hostname, func(contexts map[string]string) { contexts[name] = value }); err != nil {
				return err
			}
			return writeContextOutput(cmd, map[string]string{"hostname": hostname, "name": name, "value": value}, runtime.OutputHints{})
		},
	}
}

func newContextUnset(m *config.Manifest) *cobra.Command {
	return &cobra.Command{
		Use:   "unset <name>",
		Short: "Clear a stored context value",
		Args:  runtime.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateContextOutput(cmd); err != nil {
				return err
			}
			name := args[0]
			if _, ok := m.Contexts[name]; !ok {
				return runtime.UsageError(cmd, fmt.Errorf("unknown context %q", name))
			}
			hostname, _, err := selectedHostEntry(cmd, true)
			if err != nil {
				return err
			}
			if err := mutateHostContext(cmd, hostname, func(contexts map[string]string) { delete(contexts, name) }); err != nil {
				return err
			}
			return writeContextOutput(cmd, map[string]string{"hostname": hostname, "name": name, "status": "unset"}, runtime.OutputHints{})
		},
	}
}

func selectedHostEntry(cmd *cobra.Command, requireStored bool) (string, config.HostEntry, error) {
	hostname, err := runtime.ResolveHost(cmd)
	if err != nil {
		return "", config.HostEntry{}, err
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		return "", config.HostEntry{}, err
	}
	entry, ok := hosts.Get(hostname)
	if !ok && requireStored {
		return "", config.HostEntry{}, runtime.NewNotAuthenticatedError()
	}
	return hostname, entry, nil
}

func mutateHostContext(cmd *cobra.Command, hostname string, mutate func(map[string]string)) error {
	return config.MutateHosts(cmd.Context(), func(hosts *config.Hosts) error {
		entry, ok := hosts.Get(hostname)
		if !ok {
			return runtime.NewNotAuthenticatedError()
		}
		if entry.Contexts == nil {
			entry.Contexts = map[string]string{}
		}
		mutate(entry.Contexts)
		hosts.Set(hostname, entry)
		return nil
	})
}

func writeContextOutput(cmd *cobra.Command, value any, hints runtime.OutputHints) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	format := rootString(cmd, "output")
	if format == "" {
		format = "table"
	}
	return runtime.FormatOutput(data, format, cmd.OutOrStdout(), hints)
}

func validateContextOutput(cmd *cobra.Command) error {
	switch format := rootString(cmd, "output"); format {
	case "", "table", "json", "yaml", "raw":
		return nil
	default:
		return runtime.UsageError(cmd, fmt.Errorf("unsupported output format %q", format))
	}
}
