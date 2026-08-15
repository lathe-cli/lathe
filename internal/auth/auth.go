package auth

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
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
	cmd.AddCommand(newLogin(m), newStatus(), newLogout())
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

type oauthDeviceStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int64  `json:"interval"`
	ExpiresIn               int64  `json:"expires_in"`
}

type oauthDeviceTokenResponse struct {
	Status       string
	Error        string
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	UserEmail    string
	UserName     string
	Contexts     map[string]string
}

func rootString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Root().PersistentFlags().GetString(name)
	return v
}

func rootBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Root().PersistentFlags().GetBool(name)
	return v
}

func oauthDeviceLogin(cmd *cobra.Command, m *config.Manifest, hostname string, provider string, insecure, noBrowser bool) (config.HostEntry, error) {
	login := m.Auth.Login
	if login == nil || login.Type != config.AuthLoginOAuthDevice {
		return config.HostEntry{}, errors.New("auth.login with type oauth_device is required for --auth-type oauth")
	}
	provider = strings.TrimSpace(provider)
	deviceHostname, _ := os.Hostname()
	if deviceHostname == "" {
		deviceHostname = "unknown-host"
	}
	deviceLabel := m.CLI.Name + " on " + deviceHostname
	fallback := map[string]string{"hostname": hostname}
	if provider != "" {
		fallback["provider"] = provider
	}
	body, err := oauthDeviceRequest(login.StartRequest, fallback, map[string]string{
		config.AuthLoginHostname:    hostname,
		config.AuthLoginProvider:    provider,
		config.AuthLoginDeviceLabel: deviceLabel,
	})
	if err != nil {
		return config.HostEntry{}, err
	}
	data, err := runtime.DoRaw(cmd.Context(), hostname, "POST", login.StartPath, body, runtime.ClientOptions{Insecure: insecure, Timeout: 10 * time.Second})
	if err != nil {
		return config.HostEntry{}, fmt.Errorf("start oauth login: %w", err)
	}
	var start oauthDeviceStartResponse
	if err := json.Unmarshal(data, &start); err != nil {
		return config.HostEntry{}, fmt.Errorf("decode oauth start response: %w", err)
	}
	if start.DeviceCode == "" {
		return config.HostEntry{}, errors.New("oauth start response missing device_code")
	}
	verificationURL := start.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = start.VerificationURI
	}
	if verificationURL == "" {
		return config.HostEntry{}, errors.New("oauth start response missing verification_uri")
	}
	fmt.Fprintf(os.Stderr, "Open this URL to authenticate: %s\n", verificationURL)
	if start.UserCode != "" {
		fmt.Fprintf(os.Stderr, "Code: %s\n", start.UserCode)
	}
	openURL := start.VerificationURIComplete
	if openURL == "" {
		openURL = verificationURL
	}
	maybeOpenBrowser(openURL, noBrowser)
	token, err := pollOAuthDeviceToken(cmd, hostname, login, start, provider, deviceLabel, insecure)
	if err != nil {
		return config.HostEntry{}, err
	}
	entry := config.HostEntry{
		AuthType:          "bearer",
		LoginType:         config.AuthLoginOAuthDevice,
		LoginProvider:     provider,
		OAuthToken:        token.AccessToken,
		OAuthRefreshToken: token.RefreshToken,
		Insecure:          insecure,
		Contexts:          token.Contexts,
	}
	if token.ExpiresIn > 0 {
		entry.OAuthExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
	}
	entry.User = token.UserEmail
	if entry.User == "" {
		entry.User = token.UserName
	}
	return entry, nil
}

func pollOAuthDeviceToken(cmd *cobra.Command, hostname string, login *config.AuthLogin, start oauthDeviceStartResponse, provider, deviceLabel string, insecure bool) (oauthDeviceTokenResponse, error) {
	expiresIn := start.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}
	interval := start.Interval
	if interval <= 0 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	body, err := oauthDeviceRequest(login.PollRequest, map[string]string{"device_code": start.DeviceCode}, map[string]string{
		config.AuthLoginHostname:    hostname,
		config.AuthLoginProvider:    provider,
		config.AuthLoginDeviceLabel: deviceLabel,
		config.AuthLoginDeviceCode:  start.DeviceCode,
	})
	if err != nil {
		return oauthDeviceTokenResponse{}, err
	}
	for {
		if time.Now().After(deadline) {
			return oauthDeviceTokenResponse{}, errors.New("oauth login expired")
		}
		data, err := runtime.DoRaw(cmd.Context(), hostname, "POST", login.TokenPath, body, runtime.ClientOptions{Insecure: insecure, Timeout: 10 * time.Second})
		var token oauthDeviceTokenResponse
		if len(data) > 0 {
			var decodeErr error
			token, decodeErr = decodeOAuthDeviceToken(data, login.PollResponse)
			if decodeErr != nil {
				if err != nil {
					return oauthDeviceTokenResponse{}, fmt.Errorf("poll oauth login: %w", err)
				}
				return oauthDeviceTokenResponse{}, fmt.Errorf("decode oauth token response: %w", decodeErr)
			}
		} else if err != nil {
			return oauthDeviceTokenResponse{}, fmt.Errorf("poll oauth login: %w", err)
		} else {
			return oauthDeviceTokenResponse{}, errors.New("decode oauth token response: empty response")
		}
		if token.AccessToken != "" {
			return token, nil
		}
		state := token.Status
		if state == "" {
			state = token.Error
		}
		switch state {
		case "pending", "authorization_pending", "":
			if err != nil && state == "" {
				return oauthDeviceTokenResponse{}, fmt.Errorf("poll oauth login: %w", err)
			}
			timer := time.NewTimer(time.Duration(interval) * time.Second)
			select {
			case <-cmd.Context().Done():
				timer.Stop()
				return oauthDeviceTokenResponse{}, cmd.Context().Err()
			case <-timer.C:
			}
		case "slow_down":
			interval += 5
			timer := time.NewTimer(time.Duration(interval) * time.Second)
			select {
			case <-cmd.Context().Done():
				timer.Stop()
				return oauthDeviceTokenResponse{}, cmd.Context().Err()
			case <-timer.C:
			}
		case "denied", "access_denied":
			return oauthDeviceTokenResponse{}, errors.New("oauth login denied")
		case "expired", "expired_token":
			return oauthDeviceTokenResponse{}, errors.New("oauth login expired")
		default:
			if err != nil {
				return oauthDeviceTokenResponse{}, fmt.Errorf("poll oauth login: %w", err)
			}
			return oauthDeviceTokenResponse{}, fmt.Errorf("oauth login failed with status %q", state)
		}
	}
}

func oauthDeviceRequest(configured, fallback, values map[string]string) (map[string]string, error) {
	if configured == nil {
		return fallback, nil
	}
	body := make(map[string]string, len(configured))
	for field, value := range configured {
		if resolved, ok := values[value]; ok {
			if resolved != "" {
				body[field] = resolved
			}
			continue
		}
		if strings.Contains(value, "${") {
			return nil, fmt.Errorf("auth.login request field %q has unsupported placeholder %q", field, value)
		}
		body[field] = value
	}
	return body, nil
}

func decodeOAuthDeviceToken(data []byte, fields config.AuthLoginPollResponse) (oauthDeviceTokenResponse, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return oauthDeviceTokenResponse{}, err
	}
	expiresIn, err := oauthInt64(raw, oauthField(fields.ExpiresIn, "expires_in"))
	if err != nil {
		return oauthDeviceTokenResponse{}, err
	}
	token := oauthDeviceTokenResponse{
		Status:       pluckString(raw, oauthField(fields.Status, "status")),
		Error:        pluckString(raw, oauthField(fields.Error, "error")),
		AccessToken:  pluckString(raw, oauthField(fields.AccessToken, "access_token")),
		RefreshToken: pluckString(raw, oauthField(fields.RefreshToken, "refresh_token")),
		ExpiresIn:    expiresIn,
		UserEmail:    pluckString(raw, oauthField(fields.UserEmail, "user.email")),
		UserName:     pluckString(raw, oauthField(fields.UserName, "user.name")),
		Contexts:     map[string]string{},
	}
	for name, path := range fields.Contexts {
		if value := strings.TrimSpace(pluckString(raw, path)); value != "" {
			token.Contexts[name] = value
		}
	}
	return token, nil
}

func oauthField(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}

func oauthInt64(raw any, path string) (int64, error) {
	value, ok := pluck(raw, path)
	if !ok || value == nil {
		return 0, nil
	}
	n, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("oauth token response field %q must be an integer", path)
	}
	return n, nil
}

func maybeOpenBrowser(rawURL string, noBrowser bool) {
	if reason := browserSkipReason(noBrowser); reason != "" {
		fmt.Fprintf(os.Stderr, "Browser not opened (%s); open the URL above manually.\n", reason)
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		fmt.Fprintln(os.Stderr, "Browser not opened (verification URL is not HTTP(S)); open the URL above manually.")
		return
	}
	name := "xdg-open"
	args := []string{rawURL}
	switch goruntime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", rawURL}
	}
	if err := startBrowserCommand(name, args...); err != nil {
		fmt.Fprintf(os.Stderr, "Browser could not be opened (%v); open the URL above manually.\n", err)
	}
}

func startBrowserCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// xdg-open may stay attached to the browser, so reap it without delaying token polling.
	go func() { _ = cmd.Wait() }()
	return nil
}

func browserSkipReason(noBrowser bool) string {
	if noBrowser {
		return "--no-browser requested"
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return "SSH session detected"
	}
	if goruntime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "headless Linux detected"
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) {
		return "non-interactive terminal"
	}
	return ""
}

func newLogin(m *config.Manifest) *cobra.Command {
	var (
		authType     string
		provider     string
		withToken    bool
		deviceAuth   bool
		noBrowser    bool
		skipValidate bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a host",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostname := rootString(cmd, "hostname")
			insecure := rootBool(cmd, "insecure")
			if hostname == "" && !withToken {
				fmt.Fprint(os.Stderr, "? Hostname: ")
				line, err := readLine(os.Stdin)
				if err != nil {
					return err
				}
				hostname = strings.TrimSpace(line)
			}
			if hostname == "" {
				return runtime.UsageError(cmd, errors.New("hostname is required (use --hostname)"))
			}
			hostname = config.NormalizeHostname(hostname)
			authType = strings.ToLower(strings.TrimSpace(authType))
			if deviceAuth {
				if cmd.Flags().Changed("auth-type") && authType != "oauth" {
					return runtime.UsageError(cmd, fmt.Errorf("--device-auth cannot be used with --auth-type %s", authType))
				}
				authType = "oauth"
			}

			entry := config.HostEntry{AuthType: authType, Insecure: insecure}
			switch authType {
			case "", "bearer":
				token, err := readToken(withToken)
				if err != nil {
					return err
				}
				if token == "" {
					return runtime.UsageError(cmd, errors.New("empty token"))
				}
				entry.OAuthToken = token
			case "apikey":
				key, err := readSecret("API key", withToken)
				if err != nil {
					return err
				}
				if key == "" {
					return runtime.UsageError(cmd, errors.New("empty API key"))
				}
				entry.APIKey = key
				entry.APIKeyHeader = m.Auth.APIKeyHeader
				if !withToken {
					header := entry.APIKeyHeader
					if header == "" {
						header = "X-API-Key"
					}
					fmt.Fprintf(os.Stderr, "? Header name [%s]: ", header)
					line, err := readLine(os.Stdin)
					if err != nil {
						return err
					}
					if h := strings.TrimSpace(line); h != "" {
						entry.APIKeyHeader = h
					}
				}
			case "basic":
				fmt.Fprint(os.Stderr, "? Username: ")
				uline, err := readLine(os.Stdin)
				if err != nil {
					return err
				}
				entry.BasicUser = strings.TrimSpace(uline)
				if entry.BasicUser == "" {
					return runtime.UsageError(cmd, errors.New("empty username"))
				}
				pass, err := readSecret("Password", false)
				if err != nil {
					return err
				}
				entry.BasicPassword = pass
			case "oauth":
				if withToken {
					return runtime.UsageError(cmd, errors.New("--with-token cannot be used with --auth-type oauth"))
				}
				var err error
				entry, err = oauthDeviceLogin(cmd, m, hostname, provider, insecure, noBrowser)
				if err != nil {
					return err
				}
			default:
				return runtime.UsageError(cmd, fmt.Errorf("unknown auth type: %q (use bearer, apikey, basic, or oauth)", authType))
			}

			if !skipValidate {
				auth, err := runtime.NewAuthFromHost(entry)
				if err != nil {
					return err
				}
				result, err := validateWithAuth(cmd.Context(), hostname, auth, m.Auth.Validate, runtime.ClientOptions{Insecure: insecure})
				if err != nil {
					if !insecure && strings.Contains(err.Error(), "tls:") {
						return fmt.Errorf("credential validation failed against %s: %w\n\nThe server uses a self-signed or non-standard certificate.\nRe-run with --insecure to skip TLS verification (the choice is persisted per host)", hostname, err)
					}
					return fmt.Errorf("credential validation failed against %s: %w", hostname, err)
				}
				if result.Username != "" {
					entry.User = result.Username
				}
				if entry.User != "" {
					fmt.Fprintf(os.Stderr, "✓ Authenticated as %s\n", entry.User)
				}
			}

			if err := config.MutateHosts(cmd.Context(), func(hosts *config.Hosts) error {
				current, _ := hosts.Get(hostname)
				contexts := maps.Clone(current.Contexts)
				if contexts == nil {
					contexts = map[string]string{}
				}
				for name, value := range entry.Contexts {
					contexts[name] = value
				}
				entry.Contexts = contexts
				hosts.Set(hostname, entry)
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "✓ Logged in to %s\n", hostname)
			return nil
		},
	}
	authTypeUsage := "Authentication type: bearer, apikey, basic, oauth"
	if m.Auth.DefaultType == "" {
		authTypeUsage = "Authentication type: bearer (default), apikey, basic, oauth"
	}
	cmd.Flags().StringVar(&authType, "auth-type", m.Auth.DefaultType, authTypeUsage)
	cmd.Flags().StringVar(&provider, "provider", "", "OAuth provider hint passed to the service")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read token/key from stdin")
	cmd.Flags().BoolVar(&deviceAuth, "device-auth", false, "Use OAuth device login")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not open a browser for OAuth device login")
	cmd.Flags().BoolVar(&skipValidate, "skip-validate", false, "Do not validate credentials against the server")
	return cmd
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "View authentication status",
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
			if hostname != "" {
				e, ok := hosts.Get(hostname)
				if !ok {
					return runtime.NewNotAuthenticatedError()
				}
				printStatus(hostname, e)
				return nil
			}
			for _, n := range names {
				e, _ := hosts.Get(n)
				printStatus(n, e)
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
			fmt.Fprintf(os.Stderr, "✓ Logged out of %s\n", hostname)
			return nil
		},
	}
}

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

func printStatus(hostname string, e config.HostEntry) {
	user := e.User
	if user == "" {
		user = fmt.Sprintf("(unknown — run `%s auth login` to validate)", config.Active().CLI.Name)
	}
	authLabel := e.AuthType
	if authLabel == "" {
		authLabel = "bearer"
	}
	credential := maskedCredential(e)
	fmt.Fprintf(os.Stdout, "%s\n  ✓ Logged in as %s\n  ✓ Auth: %s\n  ✓ Credential: %s\n", hostname, user, authLabel, credential)
	if e.LoginType != "" {
		loginLabel := e.LoginType
		if e.LoginProvider != "" {
			loginLabel += " (" + e.LoginProvider + ")"
		}
		fmt.Fprintf(os.Stdout, "  ✓ Login: %s\n", loginLabel)
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

func readSecret(prompt string, fromStdin bool) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "? Paste your %s: ", prompt)
		s, err := readPasswordRobust()
		fmt.Fprintln(os.Stderr)
		return s, err
	}
	line, err := readLine(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func readToken(fromStdin bool) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "? Paste your authentication token: ")
		s, err := readPasswordRobust()
		fmt.Fprintln(os.Stderr)
		return s, err
	}
	line, err := readLine(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// readPasswordRobust reads a secret from the terminal without echo.
// Unlike term.ReadPassword, it uses raw mode and treats both \r and \n as
// line terminators. This avoids a hang on systems where ICRNL (CR→NL
// translation) is unreliable (e.g. Darwin 25 / macOS 16).
func readPasswordRobust() (s string, err error) {
	fd := int(os.Stdin.Fd())
	state, rawErr := term.MakeRaw(fd)
	if rawErr != nil {
		// Fallback: term.ReadPassword relies on ICRNL but is better than nothing.
		b, err := term.ReadPassword(fd)
		return strings.TrimSpace(string(b)), err
	}
	defer func() {
		if rerr := term.Restore(fd, state); rerr != nil && err == nil {
			err = rerr
		}
	}()

	var buf []byte
	scratch := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(scratch)
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		switch scratch[0] {
		case '\r', '\n':
			return strings.TrimSpace(string(buf)), nil
		case 3: // Ctrl+C
			return "", errors.New("interrupted")
		case 4: // Ctrl+D — EOF in raw mode (tty no longer translates it)
			return "", errors.New("interrupted")
		case 127, '\b': // DEL / backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		default:
			buf = append(buf, scratch[0])
		}
	}
	return strings.TrimSpace(string(buf)), nil
}

func readLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}
