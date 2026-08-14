package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollUntilDone_ImmediateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "done"})
	}))
	defer srv.Close()

	data, err := PollUntilDone(context.Background(), srv.URL, "/status", ClientOptions{Timeout: 5 * time.Second}, 10*time.Second)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "done" {
		t.Errorf("got status %q, want done", result["status"])
	}
}

func TestPollUntilDone_EventualSuccess(t *testing.T) {
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&call, 1)
		if n < 3 {
			w.Header().Set("Location", "/status")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"status":"pending"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "done"})
	}))
	defer srv.Close()

	data, err := PollUntilDone(context.Background(), srv.URL, "/status", ClientOptions{Timeout: 5 * time.Second}, 30*time.Second)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "done" {
		t.Errorf("got status %q, want done", result["status"])
	}
	if atomic.LoadInt32(&call) != 3 {
		t.Errorf("made %d requests, want 3", atomic.LoadInt32(&call))
	}
}

func TestPollUntilDone_AcceptsSameHostAbsoluteLocation(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := PollUntilDone(context.Background(), srv.URL, srv.URL+"/status?job=1", ClientOptions{Timeout: 5 * time.Second}, 30*time.Second); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	if gotPath != "/status?job=1" {
		t.Errorf("poll path = %q, want /status?job=1", gotPath)
	}
}

func TestPollUntilDone_AcceptsDefaultPortAbsoluteLocation(t *testing.T) {
	var gotHost string
	opts := ClientOptions{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}

	if _, err := PollUntilDone(context.Background(), "api.example.com", "https://api.example.com:443/status", opts, 30*time.Second); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	if gotHost != "api.example.com" {
		t.Errorf("host = %q, want api.example.com", gotHost)
	}
}

func TestPollUntilDone_RejectsCrossHostAbsoluteLocation(t *testing.T) {
	const querySecret = "poll-location-secret"
	const password = "poll-userinfo-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://user:"+password+"@example.com/status?sig="+querySecret)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	opts := ClientOptions{Timeout: 5 * time.Second}
	_, err := PollUntilDone(context.Background(), srv.URL, "/status", opts, 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "cross-host polling location") {
		t.Fatalf("PollUntilDone error = %v, want cross-host polling location", err)
	}
	if strings.Contains(err.Error(), querySecret) || strings.Contains(err.Error(), password) {
		t.Fatalf("PollUntilDone error leaked Location credential: %v", err)
	}
	if !strings.Contains(err.Error(), "user:xxxxx@example.com") {
		t.Fatalf("PollUntilDone error did not redact userinfo password: %v", err)
	}
}

func TestPollUntilDone_RejectsCrossHostRedirect(t *testing.T) {
	reached := make(chan string, 1)
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/status", http.StatusFound)
	}))
	defer trusted.Close()

	_, err := PollUntilDone(context.Background(), trusted.URL, "/status", ClientOptions{
		Auth:    APIKeyAuth{Key: "secret"},
		Timeout: 5 * time.Second,
	}, 30*time.Second)
	if err == nil {
		t.Error("PollUntilDone followed a cross-host redirect")
	}
	select {
	case key := <-reached:
		t.Errorf("cross-host redirect received X-API-Key %q", key)
	default:
	}
}

func TestPollUntilDone_RelativeLocationCannotChangeAuthority(t *testing.T) {
	var gotHost, gotPath, gotAuth string
	opts := ClientOptions{
		Auth: BearerAuth{Token: "secret"},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotHost = r.URL.Host
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
	}

	if _, err := PollUntilDone(context.Background(), "trusted.example", "@attacker.example/status", opts, 30*time.Second); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	if gotHost != "trusted.example" || gotPath != "/@attacker.example/status" {
		t.Fatalf("poll target = %q%s, want trusted.example/@attacker.example/status", gotHost, gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("same-origin authorization = %q", gotAuth)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPollUntilDone_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/status")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"pending"}`))
	}))
	defer srv.Close()

	_, err := PollUntilDone(context.Background(), srv.URL, "/status", ClientOptions{Timeout: 5 * time.Second}, 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestPollUntilDone_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/status")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"pending"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	_, err := PollUntilDone(ctx, srv.URL, "/status", ClientOptions{Timeout: 5 * time.Second}, 30*time.Second)
	if err == nil {
		t.Fatal("expected context error")
	}
}
