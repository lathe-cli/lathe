package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/spf13/cobra"
)

const cloudflareAccessTokenHeader = "Cf-Access-Token"
const accessTokenLimit = 32 << 10
const accessTokenTimeout = 8 * time.Second

type accessSessionKey struct{}

type accessSession struct {
	mu      sync.Mutex
	lookups map[string]*accessLookup
}

type accessLookup struct {
	done  chan struct{}
	token accessToken
	err   error
}

type accessToken struct {
	value   string
	expires int64
}

type cloudflareAccess struct {
	origin  string
	session *accessSession
}

func WithAccessSession(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(accessSessionKey{}).(*accessSession); ok {
		return ctx
	}
	return context.WithValue(ctx, accessSessionKey{}, &accessSession{lookups: map[string]*accessLookup{}})
}

func CloudflareAccessLoginAvailable(root *cobra.Command) bool {
	catalog := BuildCatalog(root, CatalogOptions{IncludeHidden: true})
	for _, command := range catalog.Commands {
		if command.Auth.Required {
			return true
		}
	}
	return false
}

func HostClientOptions(ctx context.Context, hostname string, entry config.HostEntry, insecure bool) (ClientOptions, error) {
	opts := ClientOptions{Insecure: entry.Insecure || insecure}
	if !entry.CloudflareAccess {
		return opts, nil
	}
	if opts.Insecure {
		return ClientOptions{}, NewError(CodeUsage, ExitUsage, "Cloudflare Access requires TLS certificate verification", "Remove --insecure and log in again without --insecure", nil)
	}
	origin, err := accessOrigin(hostname)
	if err != nil {
		return ClientOptions{}, NewError(CodeUsage, ExitUsage, "Cloudflare Access requires a valid HTTPS origin", "Use an HTTPS hostname and optional port without credentials, a path, a query, or a fragment", nil)
	}
	session := WithAccessSession(ctx).Value(accessSessionKey{}).(*accessSession)
	opts.access = &cloudflareAccess{origin: origin, session: session}
	return opts, nil
}

func accessOrigin(hostname string) (string, error) {
	base := strings.TrimSpace(hostname)
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("cloudflare Access requires an HTTPS hostname without a path, credentials, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return "", errors.New("cloudflare Access requires a valid HTTPS hostname")
			}
		}
		for _, r := range host {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			default:
				return "", errors.New("cloudflare Access requires a valid HTTPS hostname")
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("cloudflare Access requires a valid HTTPS port")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("cloudflare Access requires a valid HTTPS port")
		}
		if n == 443 {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, nil
}

func (a *cloudflareAccess) token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.session.mu.Lock()
	lookup, exists := a.session.lookups[a.origin]
	if !exists {
		lookup = &accessLookup{done: make(chan struct{})}
		a.session.lookups[a.origin] = lookup
	}
	a.session.mu.Unlock()
	if !exists {
		go func() {
			token, err := readAccessToken(context.WithoutCancel(ctx), a.origin)
			a.session.mu.Lock()
			lookup.token, lookup.err = token, err
			close(lookup.done)
			a.session.mu.Unlock()
		}()
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-lookup.done:
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if lookup.err != nil {
		return "", lookup.err
	}
	if time.Now().Unix() >= lookup.token.expires {
		return "", accessFailure(a.origin, true, "Cloudflare Access token expired")
	}
	return lookup.token.value, nil
}

func readAccessToken(ctx context.Context, origin string) (accessToken, error) {
	tokenCtx, cancel := context.WithTimeout(ctx, accessTokenTimeout)
	defer cancel()
	cmd := exec.CommandContext(tokenCtx, "cloudflared", "access", "token", "-app="+origin)
	cmd.WaitDelay = time.Second
	var out accessOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return accessToken{}, ctx.Err()
		}
		message := "Could not get a Cloudflare Access token"
		if errors.Is(err, exec.ErrNotFound) {
			message = "cloudflared is not installed or is not on PATH"
		} else if tokenCtx.Err() != nil {
			message = "cloudflared token lookup timed out"
		}
		return accessToken{}, accessFailure(origin, true, message)
	}
	if out.overflow {
		return accessToken{}, accessFailure(origin, true, "cloudflared returned an oversized token")
	}
	value := strings.TrimSpace(out.buffer.String())
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" || strings.ContainsAny(value, " \t\r\n") {
		return accessToken{}, accessFailure(origin, true, "cloudflared returned an empty or invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Expires <= time.Now().Unix() {
		return accessToken{}, accessFailure(origin, true, "Cloudflare Access token is invalid or expired")
	}
	return accessToken{value: value, expires: claims.Expires}, nil
}

type accessOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *accessOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := accessTokenLimit - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func accessFailure(origin string, enabled bool, message string) *LatheError {
	hint := fmt.Sprintf("Install cloudflared if needed. Run `cloudflared access login --quiet %s`", origin)
	if !enabled {
		u, _ := url.Parse(origin)
		hint += fmt.Sprintf(", then run `%s auth login --hostname %s --cloudflare-access`", filepath.Base(os.Args[0]), u.Host)
		hint += fmt.Sprintf(". Use `--hostname %s` for API commands", u.Host)
	} else {
		hint += ", then run the CLI command again"
	}
	return NewError(CodeAPIError, ExitAPIError, message, hint, nil)
}

func accessLoginRedirect(resp *http.Response) bool {
	if resp.StatusCode != http.StatusFound {
		return false
	}
	location, err := resp.Location()
	return err == nil && location.User == nil && strings.HasSuffix(strings.ToLower(location.Hostname()), ".cloudflareaccess.com")
}

type accessTransport struct {
	inner    http.RoundTripper
	access   *cloudflareAccess
	insecure bool
}

func (t *accessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.insecure {
		return nil, NewError(CodeUsage, ExitUsage, "Cloudflare Access requires TLS certificate verification", "Remove --insecure and log in again without --insecure", nil)
	}
	origin, err := accessOrigin(req.URL.Scheme + "://" + req.URL.Host)
	if err != nil || origin != t.access.origin || req.URL.User != nil || (req.Host != "" && req.Host != req.URL.Host) {
		return nil, NewError(CodeAPIError, ExitAPIError, "Cloudflare Access blocked a request outside the configured HTTPS origin", "Use an endpoint on the configured HTTPS origin", nil)
	}
	token, err := t.access.token(req.Context())
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.Header.Set(cloudflareAccessTokenHeader, token)
	resp, err := t.inner.RoundTrip(clone)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, NewError(CodeAPIError, ExitAPIError, "HTTPS request to the Cloudflare Access origin failed", "Check the hostname, network connectivity, and TLS certificate settings", nil)
	}
	if accessLoginRedirect(resp) {
		resp.Body.Close()
		return nil, accessFailure(origin, true, "Cloudflare Access rejected the token")
	}
	return resp, nil
}

func (a *cloudflareAccess) redact(data []byte) []byte {
	a.session.mu.Lock()
	defer a.session.mu.Unlock()
	for _, lookup := range a.session.lookups {
		if lookup.token.value != "" {
			data = bytes.ReplaceAll(data, []byte(lookup.token.value), []byte("***"))
		}
	}
	return data
}

func checkAccessRedirect(req *http.Request, via []*http.Request, opts ClientOptions) error {
	if opts.access != nil {
		origin, err := accessOrigin(req.URL.Scheme + "://" + req.URL.Host)
		if err != nil || origin != opts.access.origin || req.URL.User != nil {
			return NewError(CodeAPIError, ExitAPIError, "Cloudflare Access blocked a redirect outside the configured HTTPS origin", "Use an endpoint on the configured HTTPS origin", nil)
		}
		return nil
	}
	if req.Response != nil && accessLoginRedirect(req.Response) && len(via) > 0 {
		if opts.AccessLoginAvailable != nil && !opts.AccessLoginAvailable() {
			return NewError(CodeAPIError, ExitAPIError, "Cloudflare Access login is required", "This CLI cannot enable Cloudflare Access in this version because no command requires API authentication", nil)
		}
		last := via[len(via)-1]
		if origin, err := accessOrigin(last.URL.Scheme + "://" + last.URL.Host); err == nil {
			return accessFailure(origin, false, "Cloudflare Access login is required")
		}
	}
	return nil
}
