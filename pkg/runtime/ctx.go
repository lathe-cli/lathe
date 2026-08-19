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

const (
	HostSourceFlag           = "flag"
	HostSourceEnv            = "env"
	HostSourceSelected       = "selected"
	HostSourceCodegenDefault = "codegen-default"
	HostSourceUnique         = "unique"
)

type HostResolution struct {
	Hostname  string
	Source    string
	Ambiguous bool
}

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

func ResolveHostWithSource(cmd *cobra.Command) (HostResolution, error) {
	return resolveHost(cmd, "")
}

func resolveHost(cmd *cobra.Command, defaultHostname string) (HostResolution, error) {
	hosts, err := config.LoadHosts()
	if err != nil {
		return HostResolution{}, err
	}
	return resolveHostFrom(cmd, hosts, defaultHostname)
}

func resolveHostFrom(cmd *cobra.Command, hosts *config.Hosts, defaultHostname string) (HostResolution, error) {
	cli := config.Active().CLI
	if h, _ := cmd.Root().PersistentFlags().GetString("hostname"); h != "" {
		return HostResolution{Hostname: config.NormalizeHostname(h), Source: HostSourceFlag}, nil
	}
	if h := os.Getenv(cli.HostEnv); h != "" {
		return HostResolution{Hostname: config.NormalizeHostname(h), Source: HostSourceEnv}, nil
	}
	return resolveConfiguredHost(hosts, defaultHostname)
}

func resolveConfiguredHost(hosts *config.Hosts, defaultHostname string) (HostResolution, error) {
	names := hosts.Names()
	ambiguous := len(names) > 1
	if selected := hosts.Selected(); selected != "" {
		return HostResolution{Hostname: selected, Source: HostSourceSelected, Ambiguous: ambiguous}, nil
	}
	if defaultHostname = config.NormalizeHostname(defaultHostname); defaultHostname != "" {
		return HostResolution{Hostname: defaultHostname, Source: HostSourceCodegenDefault, Ambiguous: ambiguous}, nil
	}
	switch len(names) {
	case 0:
		return HostResolution{}, NewNotAuthenticatedError()
	case 1:
		return HostResolution{Hostname: names[0], Source: HostSourceUnique}, nil
	default:
		cli := config.Active().CLI
		return HostResolution{}, NewError(
			CodeGeneral,
			ExitGeneral,
			fmt.Sprintf("multiple hosts configured (%s)", strings.Join(names, ", ")),
			fmt.Sprintf("select one with `%s auth use <host>`, or pass --hostname or $%s", cli.Name, cli.HostEnv),
			nil,
		)
	}
}

type hostReporter struct {
	noticed map[string]bool
}

func (r *hostReporter) noticeImplicitHost(w io.Writer, res HostResolution) {
	if w == nil || !res.Ambiguous || res.Hostname == "" {
		return
	}
	if r.noticed == nil {
		r.noticed = map[string]bool{}
	}
	if r.noticed[res.Hostname] {
		return
	}
	r.noticed[res.Hostname] = true
	fmt.Fprintf(w, "current host: %s\n", res.Hostname)
}

// LoadHostOptions resolves hostname and client options (including auth) for
// the current command in one call. The persistent --insecure flag forces
// insecure when set; otherwise the host record's persisted Insecure value
// applies.
func LoadHostOptions(cmd *cobra.Command) (string, ClientOptions, error) {
	res, opts, err := loadHostOptions(cmd, "", true)
	return res.Hostname, opts, err
}

func loadHostOptions(cmd *cobra.Command, defaultHostname string, refresh bool) (HostResolution, ClientOptions, error) {
	hosts, err := config.LoadHosts()
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	res, err := resolveHostFrom(cmd, hosts, defaultHostname)
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	e, ok := hosts.Get(res.Hostname)
	if !ok {
		return res, ClientOptions{}, notAuthenticatedToHost(res.Hostname)
	}
	insecure := e.Insecure
	if v, err := cmd.Root().PersistentFlags().GetBool("insecure"); err == nil && v {
		insecure = true
	}
	if refresh {
		e, err = refreshHostAuthIfNeeded(cmd.Context(), res.Hostname, e, insecure)
		if err != nil {
			return res, ClientOptions{}, err
		}
	}
	auth, err := NewAuthFromHost(e)
	if err != nil {
		return res, ClientOptions{}, err
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
	res, opts, err := tryLoadHostOptions(cmd, "", true)
	return res.Hostname, opts, err
}

func tryLoadHostOptions(cmd *cobra.Command, defaultHostname string, refresh bool) (HostResolution, ClientOptions, error) {
	hosts, err := config.LoadHosts()
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
	}
	res, err := resolveHostFrom(cmd, hosts, defaultHostname)
	if err != nil {
		return HostResolution{}, ClientOptions{}, err
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
