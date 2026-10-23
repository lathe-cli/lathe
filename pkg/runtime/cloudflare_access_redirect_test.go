package runtime

import (
	"errors"
	"fmt"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/spf13/cobra"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAccessRedirectIsolation(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		for _, target := range []string{"/done", "https://other.example/done", "https://sub.api.example/done", "https://api.example:444/done", "http://api.example/done"} {
			t.Run(fmt.Sprintf("%d/%s", code, target), func(t *testing.T) {
				log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
				var seen []string
				opts := accessOptions(t, WithAccessSession(t.Context()), "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
					seen = append(seen, r.URL.String())
					if r.Header.Get("cf-access-token") != os.Getenv("ACCESS_TEST_TOKEN") || r.Header.Get("Authorization") != "Bearer api-token" {
						t.Error("missing credentials")
					}
					status := 200
					header := http.Header{}
					if r.URL.Path == "/start" {
						status = code
						header.Set("Location", target)
					}
					return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
				}))
				opts.Auth = BearerAuth{Token: "api-token"}
				_, err := DoRaw(t.Context(), "api.example", "POST", "/start", map[string]int{"x": 1}, opts)
				if target == "/done" {
					if err != nil || len(seen) != 2 {
						t.Fatalf("same-origin error=%v requests=%v", err, seen)
					}
				} else if err == nil || len(seen) != 1 {
					t.Fatalf("cross-origin error=%v requests=%v", err, seen)
				}
				if len(accessCalls(t, log)) != 1 {
					t.Fatal("token lookup repeated")
				}
			})
		}
	}
}

func TestAccessRedirectHintWithoutOptIn(t *testing.T) {
	log := fakeAccessCommand(t, "exit 1")
	for _, location := range []string{"https://team.cloudflareaccess.com/cdn-cgi/access/login", "https://team.cloudflareaccess.com.evil.example/login", "https://notcloudflareaccess.com/login"} {
		var calls int
		opts := ClientOptions{MaxRetries: -1, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls > 1 {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: r}, nil
			}
			return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{location}}, Request: r}, nil
		})}
		_, err := DoRaw(t.Context(), "api.example", "GET", "/", nil, opts)
		if strings.Contains(location, "team.cloudflareaccess.com/cdn-cgi") {
			var le *LatheError
			if calls != 1 || !errors.As(err, &le) || !strings.Contains(le.Hint, "auth login --hostname api.example --cloudflare-access") {
				t.Fatalf("missing Access hint: calls=%d error=%v", calls, err)
			}
		} else if err != nil || calls != 2 {
			t.Fatalf("unrelated redirect changed: calls=%d error=%v", calls, err)
		}
	}
	if len(accessCalls(t, log)) != 0 {
		t.Fatal("opt-in inferred from redirect")
	}
}

func TestAccessRedirectHintUsesProtectedHop(t *testing.T) {
	log := fakeAccessCommand(t, "exit 1")
	opts := ClientOptions{MaxRetries: -1, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		location := "https://real.example:8443/api"
		code := 301
		if r.URL.Host == "real.example:8443" {
			location = "https://team.cloudflareaccess.com/cdn-cgi/access/login"
			code = 302
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	_, err := DoRaw(t.Context(), "api.example", "GET", "/", nil, opts)
	var le *LatheError
	if !errors.As(err, &le) || !strings.Contains(le.Hint, "cloudflared access login --quiet https://real.example:8443") || !strings.Contains(le.Hint, "auth login --hostname real.example:8443 --cloudflare-access") || strings.Contains(le.Hint, "api.example") {
		t.Fatalf("hint targets the wrong origin: %v", err)
	}
	if len(accessCalls(t, log)) != 0 {
		t.Fatal("redirect hint executed cloudflared")
	}
}

func TestAccessComposesWithHTMLResponseGuard(t *testing.T) {
	for _, target := range []string{"/signin", "https://other.example/signin"} {
		t.Run(target, func(t *testing.T) {
			fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
			sent := 0
			opts := accessOptions(t, WithAccessSession(t.Context()), "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
				sent++
				if r.URL.Path == "/api" {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader("<html>Sign in</html>")), Request: r}, nil
			}))
			_, err := InvokeOperation(t.Context(), CommandSpec{Method: "GET", PathTpl: "/api"}, OperationInput{}, OperationOptions{Hostname: "api.example", Client: opts})
			var le *LatheError
			if !errors.As(err, &le) || le.ExitCode != ExitAPIError {
				t.Fatalf("guard did not reject: %v", err)
			}
			if target == "/signin" {
				if sent != 2 || le.Detail != "received an HTML page instead of an API response" {
					t.Fatalf("same-origin guard: sent=%d error=%v", sent, err)
				}
			} else if sent != 1 || !strings.Contains(le.Message, "blocked a redirect") {
				t.Fatalf("cross-origin guard: sent=%d error=%v", sent, err)
			}
		})
	}
}

func TestAccessUsesSharedRedirectPolicy(t *testing.T) {
	for _, mode := range []string{"callback", "limit", "reserved-auth-header"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeAccessCommand(t, "printf '%s' \"$ACCESS_TEST_TOKEN\"")
			sent, callbacks := 0, 0
			opts := accessOptions(t, WithAccessSession(t.Context()), "api.example", roundTripFunc(func(r *http.Request) (*http.Response, error) {
				sent++
				if r.Header.Get("Cf-Access-Token") == "" || r.Header.Get("X-API-Key") != "synthetic" {
					t.Error("same-origin attempt lost Access or API authentication")
				}
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/again"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}))
			opts.Auth = APIKeyAuth{Key: "synthetic"}
			opts.checkRedirect = func(*http.Request, []*http.Request) error {
				callbacks++
				if mode == "callback" {
					return http.ErrUseLastResponse
				}
				return nil
			}
			if mode == "reserved-auth-header" {
				opts.Auth = APIKeyAuth{Key: "synthetic", Header: "User-Agent"}
			}
			_, err := DoRaw(t.Context(), "api.example", "GET", "/", nil, opts)
			if err == nil {
				t.Fatal("redirect restriction did not stop the request")
			}
			wantSent, wantCallbacks, wantLookups := 1, 1, 1
			switch mode {
			case "limit":
				wantSent, wantCallbacks = 10, 9
			case "reserved-auth-header":
				wantSent, wantCallbacks, wantLookups = 0, 0, 0
			}
			if sent != wantSent || callbacks != wantCallbacks || len(accessCalls(t, log)) != wantLookups {
				t.Fatalf("sent=%d callbacks=%d lookups=%d", sent, callbacks, len(accessCalls(t, log)))
			}
		})
	}
}

func TestAccessHintForPublicOnlyCLI(t *testing.T) {
	log := fakeAccessCommand(t, "exit 1")
	bindTestManifest(t, "access-hint", "ACCESS_HINT_HOST")
	t.Setenv("ACCESS-HINT_CONFIG_DIR", t.TempDir())
	for _, mode := range []string{"public", "empty", "hidden-private", "workflow-private"} {
		publicOnly := mode == "public" || mode == "empty"
		root := &cobra.Command{Use: "access-hint"}
		root.SetContext(t.Context())
		root.PersistentFlags().String("hostname", "api.example", "")
		specs := []CommandSpec{{Group: "API", Use: "get", Method: "GET", PathTpl: "/", Security: &SecurityHint{Public: true}}}
		if mode == "empty" {
			specs = nil
		}
		if mode == "hidden-private" {
			specs = append(specs, CommandSpec{Group: "API", Use: "private", Method: "GET", PathTpl: "/private", Hidden: true})
		}
		if err := BuildFlat(root, "api", specs); err != nil {
			t.Fatal(err)
		}
		if mode == "workflow-private" {
			if err := BuildWorkflows(root, []WorkflowSpec{{Use: "work", Steps: []WorkflowStepSpec{{ID: "one", Operation: CommandSpec{Method: "GET", PathTpl: "/private"}}}}}); err != nil {
				t.Fatal(err)
			}
		}
		host, opts, err := TryLoadHostOptions(root)
		if err != nil {
			t.Fatal(err)
		}
		opts.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://team.cloudflareaccess.com/cdn-cgi/access/login"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})
		_, err = DoRaw(root.Context(), host, "GET", "/", nil, opts)
		var le *LatheError
		if !errors.As(err, &le) {
			t.Fatalf("no actionable error: %v", err)
		}
		if publicOnly {
			if strings.Contains(le.Hint, "auth login") || strings.Contains(le.Hint, "cloudflared access login") || !strings.Contains(le.Hint, "cannot enable Cloudflare Access") {
				t.Fatalf("public-only CLI offered unsupported setup: %q", le.Hint)
			}
		} else if !strings.Contains(le.Hint, "auth login --hostname api.example --cloudflare-access") {
			t.Fatalf("mixed CLI lost supported setup: %q", le.Hint)
		}
	}
	if len(accessCalls(t, log)) != 0 {
		t.Fatal("hint acquired a token")
	}
}

func TestAccessHintForPublicCLIRefresh(t *testing.T) {
	log := fakeAccessCommand(t, "exit 1")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://team.cloudflareaccess.com/cdn-cgi/access/login", http.StatusFound)
	}))
	defer srv.Close()
	config.Bind(&config.Manifest{
		CLI:  config.CLIInfo{Name: "access-refresh-hint", ConfigDirEnv: "ACCESS_REFRESH_HINT_CONFIG"},
		Auth: config.AuthInfo{Login: &config.AuthLogin{Type: config.AuthLoginOAuthDevice, RefreshPath: "/refresh"}},
	})
	t.Setenv("ACCESS_REFRESH_HINT_CONFIG", t.TempDir())
	for _, mode := range []string{"proactive", "reactive"} {
		root := &cobra.Command{Use: "access-refresh-hint"}
		root.SetContext(t.Context())
		root.PersistentFlags().String("hostname", srv.URL, "")
		if err := BuildFlat(root, "api", []CommandSpec{{Group: "API", Use: "get", Method: "GET", PathTpl: "/", Security: &SecurityHint{Public: true}}}); err != nil {
			t.Fatal(err)
		}
		hosts, err := config.LoadHosts()
		if err != nil {
			t.Fatal(err)
		}
		entry := config.HostEntry{AuthType: "bearer", OAuthToken: "api-old", OAuthRefreshToken: "refresh", Insecure: true}
		if mode == "proactive" {
			entry.OAuthExpiresAt = 1
		}
		hosts.Set(srv.URL, entry)
		if err := hosts.Save(); err != nil {
			t.Fatal(err)
		}
		_, opts, err := LoadHostOptions(root)
		if mode == "reactive" {
			if err != nil || opts.RefreshAuth == nil {
				t.Fatalf("reactive setup: %v", err)
			}
			_, err = opts.RefreshAuth(root.Context())
		}
		var le *LatheError
		if !errors.As(err, &le) || !strings.Contains(le.Hint, "cannot enable Cloudflare Access") || strings.Contains(le.Hint, "auth login") {
			t.Fatalf("%s refresh offered unsupported setup: %v", mode, err)
		}
	}
	if len(accessCalls(t, log)) != 0 {
		t.Fatal("refresh hint acquired a token")
	}
}
