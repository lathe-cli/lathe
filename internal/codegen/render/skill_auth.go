package render

import (
	"fmt"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
)

func renderAuthLoginGuidance(cli string, manifest *config.Manifest, extraFlags, hostname string) string {
	login := manifest.Auth.Login
	if login == nil || login.Type != config.AuthLoginOAuthDevice {
		return ""
	}
	var b strings.Builder
	flags := strings.TrimSpace(extraFlags)
	if manifest.Auth.DefaultType != "oauth" {
		flags = strings.TrimSpace(flags + " --device-auth")
	}
	if flags != "" {
		flags += " "
	}
	fmt.Fprintf(&b, "Run `%s auth login %s--hostname %s` for OAuth device login. ", cli, flags, hostname)
	if manifest.Auth.DefaultType != "oauth" {
		b.WriteString("Keep `--device-auth` to select device login. ")
	}
	b.WriteString("The browser opens by default in an interactive terminal; use `--no-browser` to keep login manual. ")
	if authLoginRequestUsesProvider(login.StartRequest, true) || authLoginRequestUsesProvider(login.PollRequest, false) {
		b.WriteString("Pass `--provider <provider>` only when the service requires it. ")
	}
	b.WriteByte('\n')
	return b.String()
}

func authLoginRequestUsesProvider(request map[string]string, fallbackIncludesProvider bool) bool {
	if request == nil {
		return fallbackIncludesProvider
	}
	for _, value := range request {
		if value == config.AuthLoginProvider {
			return true
		}
	}
	return false
}
