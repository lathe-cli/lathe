package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestInvokeOperation_RedactsQueryCredentialFromHTTPError(t *testing.T) {
	const secret = "ordinary-error-secret"
	var gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.URL.Query().Get("key")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := InvokeOperation(context.Background(), CommandSpec{
		Method:  "GET",
		PathTpl: "/fail",
		Params: []ParamSpec{{
			Name: "key", Flag: "key", In: InQuery, GoType: "string",
		}},
	}, OperationInput{
		Values:  map[string]any{"key": secret},
		Changed: map[string]bool{"key": true},
	}, OperationOptions{Hostname: srv.URL, Client: ClientOptions{MaxRetries: -1}})
	if err == nil {
		t.Fatal("InvokeOperation returned nil error")
	}
	if gotSecret != secret {
		t.Fatalf("server query credential = %q", gotSecret)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("HTTP error leaked query credential: %v", err)
	}
}

func TestInvokeOperation_RedactsQueryCredentialFromTransportError(t *testing.T) {
	const secret = "transport-error-secret"
	_, err := InvokeOperation(context.Background(), CommandSpec{
		Method:  "GET",
		PathTpl: "/fail",
		Params: []ParamSpec{{
			Name: "opaque", Flag: "opaque", In: InQuery, GoType: "string", Format: "password",
		}},
	}, OperationInput{
		Values:  map[string]any{"opaque": secret},
		Changed: map[string]bool{"opaque": true},
	}, OperationOptions{
		Hostname: "example.com",
		Client: ClientOptions{
			MaxRetries: -1,
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial failed")
			}),
		},
	})
	if err == nil {
		t.Fatal("InvokeOperation returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("transport error leaked query credential: %v", err)
	}
}

func TestRuntimeBodySchema_RedactsMappedSensitiveQuery(t *testing.T) {
	const secret = "runtime-schema-secret"
	var gotSecret string
	source := CommandSpec{
		Method: "GET", PathTpl: "/schema",
		Params: []ParamSpec{{Name: "id", Flag: "id", In: InQuery, GoType: "string"}},
	}
	target := CommandSpec{
		Params: []ParamSpec{{Name: "password", Flag: "password", In: InQuery, GoType: "string", Format: "password"}},
		RequestBody: &RequestBody{RuntimeSchema: &RuntimeSchemaSource{
			Operation: source, ResponsePath: "schema", Params: map[string]string{"id": "${params.password}"},
		}},
	}
	err := validateRuntimeRequestBody(context.Background(), target, OperationInput{
		Values: map[string]any{"password": secret}, Changed: map[string]bool{"password": true},
	}, map[string]any{}, OperationOptions{Hostname: "example.com", Client: ClientOptions{
		MaxRetries: -1,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotSecret = req.URL.Query().Get("id")
			return nil, errors.New("dial failed")
		}),
	}})
	if err == nil {
		t.Fatal("runtime schema request returned nil error")
	}
	if gotSecret != secret {
		t.Fatalf("runtime schema query = %q", gotSecret)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("runtime schema error leaked mapped credential: %v", err)
	}
}

func TestDoRaw_RedactsMalformedRedirectLocation(t *testing.T) {
	const password = "redirect-userinfo-secret"
	const signature = "redirect-signature-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://user:"+password+"%zz@example.com/path?sig="+signature)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	_, err := DoRaw(context.Background(), srv.URL, http.MethodGet, "/", nil, ClientOptions{MaxRetries: -1})
	if err == nil {
		t.Fatal("DoRaw returned nil error")
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), signature) {
		t.Fatalf("redirect error leaked Location credential: %v", err)
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

func TestDoRawFull_StreamCancellationClosesResponse(t *testing.T) {
	firstSent := make(chan struct{})
	requestCanceled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(firstSent)
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := doRawFull(ctx, srv.URL, http.MethodGet, "/events", nil, ClientOptions{Timeout: 5 * time.Second}, io.Discard)
		errCh <- err
	}()
	select {
	case <-firstSent:
	case <-time.After(time.Second):
		t.Fatal("server did not send first event")
	}
	cancel()
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("server request context was not canceled")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream call did not return after cancellation")
	}
}
