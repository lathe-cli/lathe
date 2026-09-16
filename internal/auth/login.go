package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

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
				token, err := readSecret("authentication token", withToken)
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

			elected := false
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
				entry.Selected = current.Selected
				hosts.Set(hostname, entry)
				if hosts.Selected() == "" {
					hosts.Select(hostname)
					elected = true
				}
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "✓ Logged in to %s\n", hostname)
			if elected {
				fmt.Fprintf(cmd.ErrOrStderr(), "✓ Now using %s\n", hostname)
			}
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

func readPasswordRobust() (s string, err error) {
	fd := int(os.Stdin.Fd())
	state, rawErr := term.MakeRaw(fd)
	if rawErr != nil {

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
		case 3:
			return "", errors.New("interrupted")
		case 4:
			return "", errors.New("interrupted")
		case 127, '\b':
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
