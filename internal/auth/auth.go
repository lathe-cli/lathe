package auth

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	}
	cmd.AddCommand(newLogin(m), newStatus(), newLogout())
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
	Status       string            `json:"status"`
	Error        string            `json:"error"`
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token"`
	ExpiresIn    int64             `json:"expires_in"`
	User         map[string]string `json:"user"`
}

func rootString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Root().PersistentFlags().GetString(name)
	return v
}

func rootBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Root().PersistentFlags().GetBool(name)
	return v
}

func oauthDeviceLogin(cmd *cobra.Command, m *config.Manifest, hostname string, provider string, insecure bool) (config.HostEntry, error) {
	login := m.Auth.Login
	if login == nil || login.Type != config.AuthLoginOAuthDevice {
		return config.HostEntry{}, errors.New("auth.login with type oauth_device is required for --auth-type oauth")
	}
	body := map[string]string{"hostname": hostname}
	provider = strings.TrimSpace(provider)
	if provider != "" {
		body["provider"] = provider
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
	token, err := pollOAuthDeviceToken(cmd, hostname, login.TokenPath, start, insecure)
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
	}
	if token.ExpiresIn > 0 {
		entry.OAuthExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
	}
	if token.User != nil {
		entry.User = token.User["email"]
		if entry.User == "" {
			entry.User = token.User["name"]
		}
	}
	return entry, nil
}

func pollOAuthDeviceToken(cmd *cobra.Command, hostname string, tokenPath string, start oauthDeviceStartResponse, insecure bool) (oauthDeviceTokenResponse, error) {
	expiresIn := start.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}
	interval := start.Interval
	if interval <= 0 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	for {
		if time.Now().After(deadline) {
			return oauthDeviceTokenResponse{}, errors.New("oauth login expired")
		}
		data, err := runtime.DoRaw(cmd.Context(), hostname, "POST", tokenPath, map[string]string{
			"device_code": start.DeviceCode,
		}, runtime.ClientOptions{Insecure: insecure, Timeout: 10 * time.Second})
		var token oauthDeviceTokenResponse
		if len(data) > 0 {
			if decodeErr := json.Unmarshal(data, &token); decodeErr != nil {
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

func newLogin(m *config.Manifest) *cobra.Command {
	var (
		authType     string
		provider     string
		withToken    bool
		deviceAuth   bool
		skipValidate bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a host",
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
				return errors.New("hostname is required (use --hostname)")
			}
			hostname = config.NormalizeHostname(hostname)
			authType = strings.ToLower(strings.TrimSpace(authType))
			if deviceAuth {
				if authType != "" && authType != "oauth" {
					return fmt.Errorf("--device-auth cannot be used with --auth-type %s", authType)
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
					return errors.New("empty token")
				}
				entry.OAuthToken = token
			case "apikey":
				key, err := readSecret("API key", withToken)
				if err != nil {
					return err
				}
				if key == "" {
					return errors.New("empty API key")
				}
				entry.APIKey = key
				if !withToken {
					fmt.Fprint(os.Stderr, "? Header name [X-API-Key]: ")
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
					return errors.New("empty username")
				}
				pass, err := readSecret("Password", false)
				if err != nil {
					return err
				}
				entry.BasicPassword = pass
			case "oauth":
				if withToken {
					return errors.New("--with-token cannot be used with --auth-type oauth")
				}
				var err error
				entry, err = oauthDeviceLogin(cmd, m, hostname, provider, insecure)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown auth type: %q (use bearer, apikey, basic, or oauth)", authType)
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

			hosts, err := config.LoadHosts()
			if err != nil {
				return err
			}
			hosts.Set(hostname, entry)
			if err := hosts.Save(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "✓ Logged in to %s\n", hostname)
			return nil
		},
	}
	cmd.Flags().StringVar(&authType, "auth-type", "", "Authentication type: bearer (default), apikey, basic, oauth")
	cmd.Flags().StringVar(&provider, "provider", "", "OAuth provider hint passed to the service")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read token/key from stdin")
	cmd.Flags().BoolVar(&deviceAuth, "device-auth", false, "Use OAuth device login")
	cmd.Flags().BoolVar(&skipValidate, "skip-validate", false, "Do not validate credentials against the server")
	return cmd
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "View authentication status",
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
					return fmt.Errorf("not logged in to %s", hostname)
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
		RunE: func(cmd *cobra.Command, args []string) error {
			hostname := rootString(cmd, "hostname")
			hosts, err := config.LoadHosts()
			if err != nil {
				return err
			}
			names := hosts.Names()
			if len(names) == 0 {
				return errors.New("not logged in to any host")
			}
			if hostname == "" {
				if len(names) == 1 {
					hostname = names[0]
				} else {
					return fmt.Errorf("multiple hosts configured, specify --hostname (have: %s)", strings.Join(names, ", "))
				}
			}
			if !hosts.Delete(hostname) {
				return fmt.Errorf("not logged in to %s", hostname)
			}
			if err := hosts.Save(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "✓ Logged out of %s\n", hostname)
			return nil
		},
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
