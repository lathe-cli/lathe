package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

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

func oauthDeviceLogin(cmd *cobra.Command, m *config.Manifest, hostname string, provider string, insecure, noBrowser bool) (config.HostEntry, error) {
	login := m.Auth.Login
	if login == nil || login.Type != config.AuthLoginOAuthDevice {
		return config.HostEntry{}, runtime.NewError(runtime.CodeUsage, runtime.ExitUsage,
			"oauth login is not configured for this CLI",
			fmt.Sprintf("use bearer, apikey, or basic auth; to enable oauth, add an auth.login block with type %q to cli.yaml and regenerate the CLI", config.AuthLoginOAuthDevice),
			errors.New("auth.login with type oauth_device is required for --auth-type oauth"))
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
