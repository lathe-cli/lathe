package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestDoRaw_SendsMethodPathAndQuery(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	data, err := DoRaw(context.Background(), srv.URL, "GET", "/users?limit=5", nil, ClientOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/users" {
		t.Errorf("path = %q, want /users", gotPath)
	}
	if gotQuery != "limit=5" {
		t.Errorf("query = %q, want limit=5", gotQuery)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("body = %s, want {\"ok\":true}", data)
	}
}

func TestDoRaw_SendsAuthorizationWhenTokenProvided(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := DoRaw(context.Background(), srv.URL, "GET", "/x", nil, ClientOptions{Auth: BearerAuth{Token: "sekret"}, Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
}

func TestDoRaw_4xxReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	_, err := DoRaw(context.Background(), srv.URL, "GET", "/missing", nil, ClientOptions{Timeout: 5 * time.Second})
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("want *HTTPError, got %T: %v", err, err)
	}
	if he.Status != http.StatusNotFound {
		t.Errorf("HTTPError.Status = %d, want 404", he.Status)
	}
	if string(he.Body) != `{"error":"not found"}` {
		t.Errorf("HTTPError.Body = %s", he.Body)
	}
}

// HTTP 401 comes from the server and must surface as *HTTPError. It is NOT the
// same as ErrNotAuthenticated (ctx.go), the local sentinel for "no host
// configured in the manifest".
func TestDoRaw_401IsNotErrNotAuthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := DoRaw(context.Background(), srv.URL, "GET", "/x", nil, ClientOptions{Timeout: 5 * time.Second})
	if errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("HTTP 401 must not wrap ErrNotAuthenticated: %v", err)
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Errorf("want *HTTPError with Status=401, got: %v", err)
	}
}

func TestDoRaw_EncodesJSONBody(t *testing.T) {
	var gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := map[string]any{"name": "alice"}
	if _, err := DoRaw(context.Background(), srv.URL, "POST", "/users", body, ClientOptions{Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if string(gotBody) != `{"name":"alice"}` {
		t.Errorf("body = %s", gotBody)
	}
}

func TestDoRaw_EncodesFormBody(t *testing.T) {
	var gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	form := url.Values{"name": {"alice"}, "age": {"30"}}
	if _, err := DoRaw(context.Background(), srv.URL, "POST", "/upload", form, ClientOptions{Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if string(gotBody) != form.Encode() {
		t.Errorf("body = %q, want %q", gotBody, form.Encode())
	}
}

func TestDoRaw_RefreshesAuthAndRetriesOnceOn401(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if len(seen) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	data, err := DoRaw(context.Background(), srv.URL, "POST", "/x", map[string]string{"a": "b"}, ClientOptions{
		Auth: BearerAuth{Token: "old"},
		RefreshAuth: func(context.Context) (Authenticator, error) {
			return BearerAuth{Token: "new"}, nil
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("data = %s", data)
	}
	if len(seen) != 2 || seen[0] != "Bearer old" || seen[1] != "Bearer new" {
		t.Fatalf("authorization sequence = %#v", seen)
	}
}
