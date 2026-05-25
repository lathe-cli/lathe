package runtime

import (
	"fmt"
	"net/http"

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

func (a APIKeyAuth) Apply(req *http.Request) error {
	if a.Key != "" {
		h := a.Header
		if h == "" {
			h = "X-API-Key"
		}
		req.Header.Set(h, a.Key)
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
