package runtime

import (
	"net/http"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
)

func TestBearerAuth_Apply(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (BearerAuth{Token: "abc"}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer abc" {
		t.Errorf("want Bearer abc, got %q", got)
	}
}

func TestBearerAuth_Empty(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (BearerAuth{}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestAPIKeyAuth_DefaultHeader(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (APIKeyAuth{Key: "k1"}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("X-API-Key"); got != "k1" {
		t.Errorf("want k1, got %q", got)
	}
}

func TestAPIKeyAuth_CustomHeader(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (APIKeyAuth{Key: "k2", Header: "Authorization"}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "k2" {
		t.Errorf("want k2, got %q", got)
	}
}

func TestAPIKeyAuth_Empty(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (APIKeyAuth{}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("X-API-Key"); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestBasicAuth_Apply(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (BasicAuth{Username: "alice", Password: "s3cret"}).Apply(req); err != nil {
		t.Fatal(err)
	}
	u, p, ok := req.BasicAuth()
	if !ok {
		t.Fatal("BasicAuth not set")
	}
	if u != "alice" || p != "s3cret" {
		t.Errorf("want alice:s3cret, got %s:%s", u, p)
	}
}

func TestBasicAuth_Empty(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (BasicAuth{}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestNoAuth_Apply(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x", nil)
	if err := (NoAuth{}).Apply(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestNewAuthFromHost(t *testing.T) {
	cases := []struct {
		name    string
		entry   config.HostEntry
		wantErr bool
		check   func(t *testing.T, a Authenticator)
	}{
		{
			name:  "empty defaults to bearer",
			entry: config.HostEntry{OAuthToken: "tok"},
			check: func(t *testing.T, a Authenticator) {
				if _, ok := a.(BearerAuth); !ok {
					t.Errorf("want BearerAuth, got %T", a)
				}
			},
		},
		{
			name:  "explicit bearer",
			entry: config.HostEntry{AuthType: "bearer", OAuthToken: "tok"},
			check: func(t *testing.T, a Authenticator) {
				if _, ok := a.(BearerAuth); !ok {
					t.Errorf("want BearerAuth, got %T", a)
				}
			},
		},
		{
			name:  "apikey",
			entry: config.HostEntry{AuthType: "apikey", APIKey: "k1", APIKeyHeader: "X-Custom"},
			check: func(t *testing.T, a Authenticator) {
				ak, ok := a.(APIKeyAuth)
				if !ok {
					t.Fatalf("want APIKeyAuth, got %T", a)
				}
				if ak.Header != "X-Custom" {
					t.Errorf("want X-Custom, got %q", ak.Header)
				}
			},
		},
		{
			name:  "basic",
			entry: config.HostEntry{AuthType: "basic", BasicUser: "u", BasicPassword: "p"},
			check: func(t *testing.T, a Authenticator) {
				if _, ok := a.(BasicAuth); !ok {
					t.Errorf("want BasicAuth, got %T", a)
				}
			},
		},
		{
			name:    "unknown type errors",
			entry:   config.HostEntry{AuthType: "mtls"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAuthFromHost(tc.entry)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, a)
		})
	}
}
