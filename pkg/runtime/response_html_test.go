package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestInvokeOperation_HTMLResponseBoundary(t *testing.T) {
	const html = "<html>private login page</html>"
	for _, tc := range []struct {
		name       string
		media      string
		actual     string
		redirect   bool
		cross      bool
		binary     bool
		stream     bool
		wantReject bool
	}{
		{name: "declared JSON direct HTML", media: "application/json", actual: "text/html"},
		{name: "vendor JSON redirect", media: "application/problem+json; charset=utf-8", actual: "Text/HTML; charset=utf-8", redirect: true, wantReject: true},
		{name: "same origin JSON redirect", media: "application/json", actual: "text/html", redirect: true, wantReject: true},
		{name: "cross origin JSON redirect", media: "application/json", actual: "text/html", redirect: true, cross: true, wantReject: true},
		{name: "undeclared same origin redirect", actual: "text/html", redirect: true, wantReject: true},
		{name: "undeclared cross origin redirect", actual: "text/html", redirect: true, cross: true, wantReject: true},
		{name: "undeclared direct HTML", actual: "text/html"},
		{name: "declared HTML redirect", media: "text/html", actual: "text/html", redirect: true},
		{name: "declared text redirect", media: "text/plain", actual: "text/html", redirect: true},
		{name: "wildcard redirect", media: "*/*", actual: "text/html", redirect: true},
		{name: "text wildcard redirect", media: "text/*", actual: "text/html", redirect: true},
		{name: "JSON wildcard redirect", media: "application/*+json", actual: "text/html", redirect: true},
		{name: "HTML alternative redirect", media: "text/html, application/vnd.demo+json", actual: "text/html", redirect: true},
		{name: "binary redirect", media: "application/octet-stream", actual: "text/html", redirect: true, binary: true},
		{name: "stream redirect", media: "text/event-stream", actual: "text/html", redirect: true, stream: true},
		{name: "undeclared stream redirect", actual: "text/html", redirect: true, stream: true},
		{name: "same origin valid JSON redirect", media: "application/json", actual: "application/json", redirect: true},
		{name: "cross origin valid JSON redirect", media: "application/json", actual: "application/json", redirect: true, cross: true},
		{name: "missing content type", media: "application/json", redirect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := html
			if tc.actual == "application/json" {
				payload = `{"ok":true}`
			}
			response := func(w http.ResponseWriter, _ *http.Request) {
				w.Header()["Content-Type"] = []string{tc.actual}
				_, _ = io.WriteString(w, payload)
			}
			dest := httptest.NewServer(http.HandlerFunc(response))
			defer dest.Close()
			src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tc.redirect || r.URL.Path == "/final" {
					response(w, r)
					return
				}
				target := "/final"
				if tc.cross {
					target = dest.URL + target
				}
				http.Redirect(w, r, target, http.StatusFound)
			}))
			defer src.Close()
			spec := CommandSpec{Method: "GET", PathTpl: "/start", Output: OutputHints{ResponseMediaType: tc.media, Binary: tc.binary}}
			if tc.stream {
				spec.Output.Streaming = &StreamingHint{}
			}
			result, err := InvokeOperation(context.Background(), spec, OperationInput{}, OperationOptions{Hostname: src.URL})
			if !tc.wantReject {
				testutil.Require(t, err == nil, "InvokeOperation: %v", err)
				testutil.Require(t, string(result.Data) == payload, "body = %q", result.Data)
				if tc.binary || tc.stream {
					var raw bytes.Buffer
					_, err = invokeOperation(context.Background(), spec, OperationInput{}, OperationOptions{Hostname: src.URL}, operationOutput{raw: &raw})
					testutil.Require(t, err == nil && raw.String() == payload, "raw body = %q, err = %v", &raw, err)
				}
				return
			}
			testutil.Require(t, err != nil, "HTML response succeeded: %q", result.Data)
			le := ClassifyError(err)
			testutil.Require(t, le.Code == CodeAPIError && le.ExitCode == ExitAPIError && le.HTTP != nil && le.HTTP.Status == 200, "error = %+v", le)
			testutil.Require(t, le.Detail != "" && le.Hint != "" && len(result.Data) == 0, "error = %+v, result = %+v", le, result)
			testutil.Require(t, !strings.Contains(strings.ToLower(le.Hint), "retry"), "hint requests a retry: %q", le.Hint)
			for _, format := range []string{"table", "json", "yaml", "raw"} {
				var out bytes.Buffer
				testutil.Require(t, FormatError(err, format, &out) == ExitAPIError, "format %s exit", format)
				testutil.Require(t, !strings.Contains(out.String(), html) && !strings.Contains(out.String(), src.URL) && !strings.Contains(out.String(), dest.URL), "unsafe error: %s", &out)
			}
		})
	}
}

func TestBuild_DirectHTMLContentTypePreservesSuccess(t *testing.T) {
	isolateRuntime(t)
	for _, tc := range []struct {
		name   string
		method string
		status int
		body   string
	}{
		{name: "POST 201 JSON", method: "POST", status: http.StatusCreated, body: `{"id":1}`},
		{name: "GET 200 JSON", method: "GET", status: http.StatusOK, body: `{"id":1}`},
		{name: "DELETE 204 empty", method: "DELETE", status: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []string{"table", "json", "raw"} {
				t.Run(format, func(t *testing.T) {
					var hits int
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						hits++
						if r.Method != tc.method {
							t.Errorf("method = %s, want %s", r.Method, tc.method)
						}
						w.Header().Set("Content-Type", "text/html; charset=UTF-8")
						w.WriteHeader(tc.status)
						_, _ = io.WriteString(w, tc.body)
					}))
					defer srv.Close()
					root := newExecutionRoot(format)
					root.SilenceUsage = true
					mustBuild(t, root, "demo", []CommandSpec{{Group: "Items", Use: "call", Method: tc.method, PathTpl: "/items", Security: &SecurityHint{Public: true}, Output: OutputHints{ResponseMediaType: "application/json"}}})
					var stdout, stderr bytes.Buffer
					root.SetOut(&stdout)
					root.SetErr(&stderr)
					root.SetArgs([]string{"--hostname", srv.URL, "demo", "items", "call"})
					testutil.Require(t, Execute(root) == ExitOK && hits == 1 && stderr.Len() == 0, "hits = %d, stdout = %s, stderr = %s", hits, &stdout, &stderr)
					if tc.body == "" {
						testutil.Require(t, stdout.Len() == 0, "stdout = %s", &stdout)
					} else if format == "json" || format == "raw" {
						var data map[string]int
						testutil.Require(t, json.Unmarshal(stdout.Bytes(), &data) == nil && data["id"] == 1, "stdout = %s", &stdout)
					}
				})
			}
		})
	}
}

func TestInvokeOperation_HTMLDuringPaginationAndWait(t *testing.T) {
	for _, mode := range []string{"pagination", "wait"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "<html>login</html>")
					return
				}
				if r.URL.Path == "/job" || r.URL.Query().Get("cursor") != "" {
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "wait" {
					w.Header().Set("Location", "/job")
					w.WriteHeader(http.StatusAccepted)
				}
				_, _ = io.WriteString(w, `{"items":[1],"next":"page2"}`)
			}))
			defer srv.Close()
			spec := CommandSpec{Method: "GET", PathTpl: "/start", Output: OutputHints{ListPath: "items", Pagination: &PaginationHint{Strategy: "cursor", TokenParam: "cursor", TokenField: "next"}}}
			result, err := InvokeOperation(context.Background(), spec, OperationInput{}, OperationOptions{Hostname: srv.URL, PaginateAll: mode == "pagination", Wait: mode == "wait"})
			testutil.Require(t, err != nil && ClassifyError(err).Code == CodeAPIError && len(result.Data) == 0, "result = %+v, err = %v", result, err)
		})
	}
}

func TestBuild_RedirectedHTMLFailsAllFormats(t *testing.T) {
	isolateRuntime(t)
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>private login page</html>")
	}))
	defer dest.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/login?token=private", http.StatusFound)
	}))
	defer src.Close()
	for _, format := range []string{"table", "json", "yaml", "raw"} {
		t.Run(format, func(t *testing.T) {
			root := newExecutionRoot(format)
			root.SilenceUsage = true
			mustBuild(t, root, "demo", []CommandSpec{{Group: "Items", Use: "list", Method: "GET", PathTpl: "/items", Security: &SecurityHint{Public: true}}})
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs([]string{"--hostname", src.URL, "demo", "items", "list"})
			testutil.Require(t, Execute(root) == ExitAPIError, "stdout = %s, stderr = %s", &stdout, &stderr)
			testutil.Require(t, stdout.Len() == 0 && !strings.Contains(stderr.String(), "private") && !strings.Contains(stderr.String(), src.URL), "stdout = %s, stderr = %s", &stdout, &stderr)
			if format == "json" {
				var envelope errorEnvelope
				testutil.Require(t, json.Unmarshal(stderr.Bytes(), &envelope) == nil && envelope.Error.Code == CodeAPIError, "stderr = %s", &stderr)
			}
		})
	}
}

func TestInvokeOperation_RedirectPoliciesRemainEffective(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()
	_, err := InvokeOperation(context.Background(), CommandSpec{Method: "GET", PathTpl: "/loop"}, OperationInput{}, OperationOptions{Hostname: srv.URL})
	testutil.Require(t, err != nil && hits == 10, "err = %v, requests = %d", err, hits)
	_, err = InvokeOperation(context.Background(), CommandSpec{Method: "GET", PathTpl: "/loop"}, OperationInput{}, OperationOptions{Hostname: srv.URL, Client: ClientOptions{checkRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
	testutil.Require(t, err != nil && ClassifyError(err).HTTP.Status == http.StatusFound && hits == 11, "err = %v, requests = %d", err, hits)
}
