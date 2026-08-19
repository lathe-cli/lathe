package runtime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

// ErrNotAuthenticated is the sentinel returned when no host is configured
// and none is selected via --hostname / $<HostEnv>. main.go maps this to
// exit code 4 via errors.Is. The user-facing wording is rendered by
// NewNotAuthenticatedError using the bound manifest.
var ErrNotAuthenticated = errors.New("not authenticated")

// NewNotAuthenticatedError renders the "no host configured" message using
// the bound manifest and wraps ErrNotAuthenticated so errors.Is still works.
func NewNotAuthenticatedError() error {
	name := config.Active().CLI.Name
	return fmt.Errorf("not logged in to any %s host; run `%s auth login` to authenticate: %w", name, name, ErrNotAuthenticated)
}

func ResolveHost(cmd *cobra.Command) (string, error) {
	res, err := resolveHost(cmd, "")
	return res.Hostname, err
}

// HostResolution is the selected hostname, how it was chosen, and how many
// hosts are configured (0 when resolution did not consult hosts.yml).
type HostResolution struct {
	Hostname   string
	Source     string
	Configured int
}

const (
	hostSourceFlag           = "flag"
	hostSourceEnv            = "env"
	hostSourcePersisted      = "persisted"
	hostSourceCodegenDefault = "codegen-default"
	hostSourceUnique         = "unique"
)

// ResolveHostWithSource returns the hostname selected for cmd plus its
// resolution source, for status and dry-run provenance.
func ResolveHostWithSource(cmd *cobra.Command) (HostResolution, error) {
	return resolveHost(cmd, "")
}

func resolveHost(cmd *cobra.Command, defaultHostname string) (HostResolution, error) {
	cli := config.Active().CLI
	if h, _ := cmd.Root().PersistentFlags().GetString("hostname"); h != "" {
		return HostResolution{Hostname: config.NormalizeHostname(h), Source: hostSourceFlag}, nil
	}
	if h := os.Getenv(cli.HostEnv); h != "" {
		return HostResolution{Hostname: config.NormalizeHostname(h), Source: hostSourceEnv}, nil
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		return HostResolution{}, err
	}
	configured := len(hosts.Names())
	if persisted := hosts.Default(); persisted != "" {
		if _, ok := hosts.Get(persisted); ok {
			return HostResolution{Hostname: persisted, Source: hostSourcePersisted, Configured: configured}, nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: persisted default host %q is not configured; clearing it\n", persisted)
		_ = config.MutateHosts(cmd.Context(), func(h *config.Hosts) error {
			if h.Default() == persisted {
				h.ClearDefault()
			}
			return nil
		})
	}
	if defaultHostname = config.NormalizeHostname(defaultHostname); defaultHostname != "" {
		return HostResolution{Hostname: defaultHostname, Source: hostSourceCodegenDefault, Configured: configured}, nil
	}
	names := hosts.Names()
	switch len(names) {
	case 0:
		return HostResolution{}, NewNotAuthenticatedError()
	case 1:
		return HostResolution{Hostname: names[0], Source: hostSourceUnique, Configured: 1}, nil
	default:
		return HostResolution{Configured: len(names)}, NewError(
			CodeGeneral,
			ExitGeneral,
			fmt.Sprintf("multiple hosts configured (%s)", strings.Join(names, ", ")),
			fmt.Sprintf("specify --hostname or $%s, or run `%s host default set <host>`", cli.HostEnv, cli.Name),
			nil,
		)
	}
}

// noticeImplicitHost announces the selected host when it was chosen
// implicitly (persisted / codegen default / unique) and a wrong-host choice
// is possible because more than one host is configured.
func noticeImplicitHost(w io.Writer, res HostResolution) {
	if w == nil || res.Hostname == "" || res.Configured <= 1 {
		return
	}
	var label string
	switch res.Source {
	case hostSourcePersisted:
		label = "persisted default"
	case hostSourceCodegenDefault:
		label = "codegen default"
	case hostSourceUnique:
		label = "unique host"
	default:
		return
	}
	fmt.Fprintf(w, "host: %s (%s)\n", res.Hostname, label)
}

// LoadHostOptions resolves hostname and client options (including auth) for
// the current command in one call. The persistent --insecure flag forces
// insecure when set; otherwise the host record's persisted Insecure value
// applies.
func LoadHostOptions(cmd *cobra.Command) (string, ClientOptions, error) {
	res, opts, err := loadHostOptions(cmd, "")
	return res.Hostname, opts, err
}

func loadHostOptions(cmd *cobra.Command, defaultHostname string) (HostResolution, ClientOptions, error) {
	return loadHostOptionsMaybeRefresh(cmd, defaultHostname, true)
}

func loadHostOptionsMaybeRefresh(cmd *cobra.Command, defaultHostname string, refresh bool) (HostResolution, ClientOptions, error) {
	res, err := resolveHost(cmd, defaultHostname)
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	e, ok := hosts.Get(res.Hostname)
	if !ok {
		return HostResolution{}, ClientOptions{}, notAuthenticatedToHost(res.Hostname)
	}
	insecure := e.Insecure
	if v, err := cmd.Root().PersistentFlags().GetBool("insecure"); err == nil && v {
		insecure = true
	}
	if refresh {
		e, err = refreshHostAuthIfNeeded(cmd.Context(), res.Hostname, e, insecure)
		if err != nil {
			return HostResolution{}, ClientOptions{}, err
		}
	}
	auth, err := NewAuthFromHost(e)
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	opts := ClientOptions{
		Auth:     auth,
		Insecure: insecure,
	}
	if refresh && canRefreshHostAuth(e) {
		opts.RefreshAuth = refreshAuthFunc(res.Hostname, insecure, e.OAuthToken)
	}
	return res, opts, nil
}

func TryLoadHostOptions(cmd *cobra.Command) (string, ClientOptions, error) {
	res, opts, err := tryLoadHostOptions(cmd, "")
	return res.Hostname, opts, err
}

func tryLoadHostOptions(cmd *cobra.Command, defaultHostname string) (HostResolution, ClientOptions, error) {
	return tryLoadHostOptionsMaybeRefresh(cmd, defaultHostname, true)
}

func tryLoadHostOptionsMaybeRefresh(cmd *cobra.Command, defaultHostname string, refresh bool) (HostResolution, ClientOptions, error) {
	res, err := resolveHost(cmd, defaultHostname)
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	hosts, err := config.LoadHosts()
	if err != nil {
		return res, ClientOptions{}, nil
	}
	e, ok := hosts.Get(res.Hostname)
	if !ok {
		opts := ClientOptions{}
		if v, err := cmd.Root().PersistentFlags().GetBool("insecure"); err == nil && v {
			opts.Insecure = true
		}
		return res, opts, nil
	}
	insecure := e.Insecure
	if v, err := cmd.Root().PersistentFlags().GetBool("insecure"); err == nil && v {
		insecure = true
	}
	if refresh {
		if refreshed, err := refreshHostAuthIfNeeded(cmd.Context(), res.Hostname, e, insecure); err == nil {
			e = refreshed
		}
	}
	auth, err := NewAuthFromHost(e)
	if err != nil {
		return res, ClientOptions{}, nil
	}
	opts := ClientOptions{
		Auth:     auth,
		Insecure: insecure,
	}
	if refresh && canRefreshHostAuth(e) {
		opts.RefreshAuth = refreshAuthFunc(res.Hostname, insecure, e.OAuthToken)
	}
	return res, opts, nil
}

func notAuthenticatedToHost(string) error {
	return fmt.Errorf("authentication required for selected host: %w", ErrNotAuthenticated)
}
