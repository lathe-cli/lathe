package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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

func rootString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Root().PersistentFlags().GetString(name)
	return v
}

func rootBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Root().PersistentFlags().GetBool(name)
	return v
}

func newLogin(m *config.Manifest) *cobra.Command {
	var (
		authType     string
		withToken    bool
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
			default:
				return fmt.Errorf("unknown auth type: %q (use bearer, apikey, or basic)", authType)
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
				entry.User = result.Username
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
	cmd.Flags().StringVar(&authType, "auth-type", "", "Authentication type: bearer (default), apikey, basic")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read token/key from stdin")
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
