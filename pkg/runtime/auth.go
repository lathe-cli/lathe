package runtime

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
)

type Authenticator interface {
	Apply(req *http.Request) error
}

type BearerAuth struct{ Token string }

func (a BearerAuth) Apply(req *http.Request) error {
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	return nil
}

type APIKeyAuth struct {
	Key    string
	Header string
}

func (a APIKeyAuth) header() string { return cmp.Or(a.Header, "X-API-Key") }

func (a APIKeyAuth) Apply(req *http.Request) error {
	if a.Key != "" {
		req.Header.Set(a.header(), a.Key)
	}
	return nil
}

type BasicAuth struct {
	Username string
	Password string
}

func (a BasicAuth) Apply(req *http.Request) error {
	if a.Username != "" {
		req.SetBasicAuth(a.Username, a.Password)
	}
	return nil
}

type NoAuth struct{}

func (NoAuth) Apply(*http.Request) error { return nil }

func securitySatisfied(h *SecurityHint, auth Authenticator, path string, headers map[string]string) bool {
	if h == nil || h.Public || len(h.Requirements) == 0 || auth == nil {
		return true
	}
	switch auth.(type) {
	case BearerAuth, BasicAuth, APIKeyAuth:
	default:
		return true
	}
	u, err := url.Parse(path)
	if err != nil {
		return true
	}
	req := &http.Request{URL: u, Header: http.Header{}}
	if err := auth.Apply(req); err != nil {
		return true
	}
	_, keyed := auth.(APIKeyAuth)
	for k, v := range headers {
		req.Header.Set(k, v)
		keyed = keyed || strings.EqualFold(k, "Authorization")
	}
	for _, r := range h.Requirements {
		if !slices.ContainsFunc(r.Schemes, func(s SecurityScheme) bool { return !schemeSatisfied(s, req, keyed) }) {
			return true
		}
	}
	return false
}

func schemeSatisfied(s SecurityScheme, req *http.Request, rawAuthorization bool) bool {
	switch s.Type {
	case "http":
		return authorizationSatisfies(req, s.Scheme, rawAuthorization)
	case "oauth2", "openIdConnect":
		return authorizationSatisfies(req, "bearer", rawAuthorization)
	case "apiKey":
		switch s.In {
		case "header":
			return req.Header.Get(s.Param) != ""
		case "query":
			return req.URL.Query().Get(s.Param) != ""
		case "cookie":
			c, err := req.Cookie(s.Param)
			return err == nil && c.Value != ""
		}
	}
	return true
}

func authorizationSatisfies(req *http.Request, scheme string, raw bool) bool {
	value := req.Header.Get("Authorization")
	if value == "" {
		return false
	}
	got, _, _ := strings.Cut(value, " ")
	return scheme == "" || raw || strings.EqualFold(got, scheme)
}

func securityAcceptance(hint *SecurityHint) string {
	if hint == nil {
		return ""
	}
	alts := make([]string, len(hint.Requirements))
	for i, req := range hint.Requirements {
		if len(req.Schemes) == 0 {
			alts[i] = "anonymous"
			continue
		}
		parts := make([]string, len(req.Schemes))
		for j, scheme := range req.Schemes {
			parts[j] = scheme.String()
		}
		alts[i] = strings.Join(parts, " + ")
	}
	return strings.Join(alts, " | ")
}

func NewAuthFromHost(e config.HostEntry) (Authenticator, error) {
	switch e.AuthType {
	case "", "bearer":
		return BearerAuth{Token: e.OAuthToken}, nil
	case "apikey":
		return APIKeyAuth{Key: e.APIKey, Header: e.APIKeyHeader}, nil
	case "basic":
		return BasicAuth{Username: e.BasicUser, Password: e.BasicPassword}, nil
	default:
		return nil, fmt.Errorf("unknown auth type: %q", e.AuthType)
	}
}
