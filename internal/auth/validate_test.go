package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestPluck(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		path string
		want string
	}{
		{"flat hit", map[string]any{"username": "alice"}, "username", "alice"},
		{"flat miss", map[string]any{"username": "alice"}, "missing", ""},
		{"nested hit", map[string]any{"data": map[string]any{"user": map[string]any{"name": "bob"}}}, "data.user.name", "bob"},
		{"nested leaf miss", map[string]any{"data": map[string]any{"user": map[string]any{}}}, "data.user.name", ""},
		{"nested mid miss", map[string]any{"data": map[string]any{}}, "data.user.name", ""},
		{"non-string value", map[string]any{"n": 42}, "n", ""},
		{"empty path", map[string]any{"username": "alice"}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pluck(tc.raw, tc.path)
			if got != tc.want {
				t.Errorf("pluck(%v, %q) = %q, want %q", tc.raw, tc.path, got, tc.want)
			}
		})
	}
}

func TestValidateToken_NilValidateSkips(t *testing.T) {
	r, err := validateWithAuth(context.Background(), "example.com", runtime.BearerAuth{Token: "t"}, nil, runtime.ClientOptions{})
	if err != nil {
		t.Fatalf("nil v should not error, got %v", err)
	}
	if r.Username != "" {
		t.Errorf("nil v should yield empty Username, got %q", r.Username)
	}
}

func TestValidateToken_PluckFlat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET (default for empty method), got %s", r.Method)
		}
		if r.URL.Path != "/whoami" {
			t.Errorf("expected /whoami, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("expected Bearer token header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"alice","uid":"u1"}`))
	}))
	defer srv.Close()

	v := &config.AuthValidate{
		Method: "",
		Path:   "/whoami",
		Display: config.AuthValidateDisplay{
			UsernameField: "username",
			FallbackField: "uid",
		},
	}
	r, err := validateWithAuth(context.Background(), srv.URL, runtime.BearerAuth{Token: "tok"}, v, runtime.ClientOptions{})
	if err != nil {
		t.Fatalf("validateWithAuth: %v", err)
	}
	if r.Username != "alice" {
		t.Errorf("want alice, got %q", r.Username)
	}
}

func TestValidateToken_FallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uid":"u1"}`))
	}))
	defer srv.Close()

	v := &config.AuthValidate{
		Path: "/",
		Display: config.AuthValidateDisplay{
			UsernameField: "username",
			FallbackField: "uid",
		},
	}
	r, err := validateWithAuth(context.Background(), srv.URL, runtime.BearerAuth{Token: "tok"}, v, runtime.ClientOptions{})
	if err != nil {
		t.Fatalf("validateWithAuth: %v", err)
	}
	if r.Username != "u1" {
		t.Errorf("want u1, got %q", r.Username)
	}
}

func TestValidateToken_NestedPluck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user":{"name":"carol"}}}`))
	}))
	defer srv.Close()

	v := &config.AuthValidate{
		Path: "/",
		Display: config.AuthValidateDisplay{
			UsernameField: "data.user.name",
		},
	}
	r, err := validateWithAuth(context.Background(), srv.URL, runtime.BearerAuth{Token: "tok"}, v, runtime.ClientOptions{})
	if err != nil {
		t.Fatalf("validateWithAuth: %v", err)
	}
	if r.Username != "carol" {
		t.Errorf("want carol, got %q", r.Username)
	}
}
