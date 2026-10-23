package runtime

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && originPort(a) == originPort(b)
}

func originPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func redirectHeaderAllowed(name string) bool {
	switch strings.ToLower(name) {
	case "accept", "accept-language", "user-agent", "range", "if-match", "if-none-match", "if-modified-since", "if-unmodified-since", "if-range", "content-type":
		return true
	default:
		return false
	}
}

func redirectPolicy(opts ClientOptions) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if err := checkAccessRedirect(req, via, opts); err != nil {
			return err
		}
		if opts.checkRedirect != nil {
			if err := opts.checkRedirect(req, via); err != nil {
				return err
			}
		}
		crossed := !sameOrigin(via[0].URL, req.URL)
		for _, prev := range via[1:] {
			crossed = crossed || !sameOrigin(via[0].URL, prev.URL)
		}
		if crossed {
			replayed := req.GetBody != nil && req.Method == via[0].Method
			for name := range req.Header {
				if !redirectHeaderAllowed(name) || strings.EqualFold(name, "Content-Type") && !replayed {
					delete(req.Header, name)
				}
			}
		}
		return nil
	}
}
