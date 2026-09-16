package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestPluck(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		path string
		want any
		ok   bool
	}{
		{"flat hit", map[string]any{"username": "alice"}, "username", "alice", true},
		{"flat miss", map[string]any{"username": "alice"}, "missing", nil, false},
		{"nested hit", map[string]any{"data": map[string]any{"user": map[string]any{"name": "bob"}}}, "data.user.name", "bob", true},
		{"nested leaf miss", map[string]any{"data": map[string]any{"user": map[string]any{}}}, "data.user.name", nil, false},
		{"nested mid miss", map[string]any{"data": map[string]any{}}, "data.user.name", nil, false},
		{"array hit", []any{map[string]any{"id": "u1"}}, "0.id", "u1", true},
		{"array miss", []any{}, "0.id", nil, false},
		{"array bad index", []any{map[string]any{"id": "u1"}}, "id", nil, false},
		{"empty path", map[string]any{"username": "alice"}, "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pluck(tc.raw, tc.path)
			testutil.Check(t, got == tc.want && ok == tc.ok, "pluck(%v, %q) = (%v, %v), want (%v, %v)", tc.raw, tc.path, got, ok, tc.want, tc.ok)
		})
	}
}

// TestPluckString pins the display contract: scalar leaves render as strings
// (JSON numbers keep their literal form), while containers and misses render
// as empty so the caller falls through to fallback_field.
func TestPluckString(t *testing.T) {
	raw := map[string]any{
		"name":   "alice",
		"id":     json.Number("9007199254740993"),
		"active": true,
		"obj":    map[string]any{"k": "v"},
		"arr":    []any{"a"},
		"null":   nil,
	}
	cases := []struct {
		path string
		want string
	}{
		{"name", "alice"},
		{"id", "9007199254740993"},
		{"active", "true"},
		{"obj", ""},
		{"arr", ""},
		{"null", ""},
		{"missing", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := pluckString(raw, tc.path); got != tc.want {
				t.Errorf("pluckString(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestValidateToken_NilValidateSkips(t *testing.T) {
	r, err := validateWithAuth(context.Background(), "example.com", runtime.BearerAuth{Token: "t"}, nil, runtime.ClientOptions{})
	testutil.Require(t, err == nil, "nil v should not error, got %v", err)
	testutil.Check(t, r.Username == "", "nil v should yield empty Username, got %q", r.Username)
}

func TestValidateToken_PluckFlat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.Check(t, r.Method == "GET", "expected GET (default for empty method), got %s", r.Method)
		testutil.Check(t, r.URL.Path == "/whoami", "expected /whoami, got %s", r.URL.Path)
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
	testutil.Require(t, err == nil, "validateWithAuth: %v", err)
	testutil.Check(t, r.Username == "alice", "want alice, got %q", r.Username)
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
	testutil.Require(t, err == nil, "validateWithAuth: %v", err)
	testutil.Check(t, r.Username == "u1", "want u1, got %q", r.Username)
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
	testutil.Require(t, err == nil, "validateWithAuth: %v", err)
	testutil.Check(t, r.Username == "carol", "want carol, got %q", r.Username)
}

// TestValidateToken_NoAssertNoDisplaySkipsDecode pins the loosening introduced
// alongside auth.validate.assert: with neither an assertion nor a display path,
// validation only proves the request succeeded and no longer rejects a
// non-JSON body.
func TestValidateToken_NoAssertNoDisplaySkipsDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("v4.0.0"))
	}))
	defer srv.Close()

	v := &config.AuthValidate{Path: "/"}
	r, err := validateWithAuth(context.Background(), srv.URL, runtime.BearerAuth{Token: "tok"}, v, runtime.ClientOptions{})
	testutil.Require(t, err == nil, "validateWithAuth: %v", err)
	testutil.Check(t, r.Username == "", "Username = %q, want empty", r.Username)
}

func TestValidateToken_Assertions(t *testing.T) {
	responses := map[string]string{
		"/array":     `[{"id":42}]`,
		"/anonymous": `{"message":"ok"}`,
		"/text":      "v4.0.0",
		"/empty":     " \n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(responses[r.URL.Path]))
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		path     string
		assert   config.AuthValidateAssert
		display  string
		wantUser string
		wantErr  bool
	}{
		{"array scalar", "/array", config.AuthValidateAssert{Field: "0.id", NonEmpty: true}, "0.id", "42", false},
		{"missing field", "/anonymous", config.AuthValidateAssert{Field: "user.id", NonEmpty: true}, "", "", true},
		{"plain text", "/text", config.AuthValidateAssert{NonEmpty: true}, "", "", false},
		{"empty body", "/empty", config.AuthValidateAssert{NonEmpty: true}, "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := &config.AuthValidate{
				Path:    tc.path,
				Assert:  &tc.assert,
				Display: config.AuthValidateDisplay{UsernameField: tc.display},
			}
			result, err := validateWithAuth(context.Background(), srv.URL, runtime.BearerAuth{Token: "tok"}, v, runtime.ClientOptions{})
			testutil.Require(t, err != nil == tc.wantErr, "validateWithAuth error = %v, wantErr %v", err, tc.wantErr)
			testutil.Require(t, result.Username == tc.wantUser, "Username = %q, want %q", result.Username, tc.wantUser)
		})
	}
}
