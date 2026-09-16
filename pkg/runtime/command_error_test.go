package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_InvalidOutputBlocksBeforeRequest(t *testing.T) {
	isolateRuntime(t)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	root := newExecutionRoot("table")
	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", []CommandSpec{{
		Group: "Items", Use: "get-item", Method: "GET", PathTpl: "/items/1", Security: &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "--output", "not-a-format", "demo", "items", "get-item"})

	err := root.Execute()
	testutil.Require(t, err != nil && ClassifyError(err).Code == CodeUsage, "error = %v, want usage", err)
	testutil.Require(t, hits == 0, "server hits = %d, want 0", hits)
}

func TestBuild_EnumUsageErrorSafeDetail(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Usage",
		Use:     "summary",
		Method:  "GET",
		PathTpl: "/usage",
		Params: []ParamSpec{
			{Name: "range", Flag: "range", In: InQuery, GoType: "string", Enum: []string{"7", "30"}},
		},
		Security: &SecurityHint{Public: true},
	}}
	le := executeCommandError(t, specs, "demo", "usage", "summary", "--range", "14")
	testutil.Require(t, le.Code == CodeUsage, "code = %q, want %q", le.Code, CodeUsage)
	testutil.Require(t, le.Detail == "--range accepts: 7, 30", "detail = %q", le.Detail)
	testutil.Require(t, !strings.Contains(le.Detail, "14"), "detail echoed user input: %q", le.Detail)
}

func TestBuild_RequiredFlagUsageErrorDetail(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "get",
		Method:  "GET",
		PathTpl: "/keys/{id}",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true},
		},
		Security: &SecurityHint{Public: true},
	}}
	le := executeCommandError(t, specs, "demo", "keys", "get")
	testutil.Require(t, le.Detail == "missing required: --id", "detail = %q", le.Detail)
}

func TestBuild_MissingBodyFieldUsageErrorDetail(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "create",
		Method:  "POST",
		PathTpl: "/keys",
		Params: []ParamSpec{
			{Name: "name", Flag: "name", In: InVariable, GoType: "string", Required: true},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
		Security:    &SecurityHint{Public: true},
	}}
	le := executeCommandError(t, specs, "demo", "keys", "create", "--set", "other=1")
	testutil.Require(t, le.Detail == "missing required: name", "detail = %q", le.Detail)
}

func TestBuild_RequiredBodyUsageErrorDetail(t *testing.T) {
	tests := []struct {
		name       string
		params     []ParamSpec
		wantDetail string
	}{
		{
			name:       "with body flags",
			params:     []ParamSpec{{Name: "name", Flag: "name", In: InBody, GoType: "string"}},
			wantDetail: "request body required: pass --file, --set, --set-str, or a body flag",
		},
		{
			name:       "without body flags",
			wantDetail: "request body required: pass --file, --set, or --set-str",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			specs := []CommandSpec{{
				Group:       "Keys",
				Use:         "create",
				Method:      "POST",
				PathTpl:     "/keys",
				Params:      tc.params,
				RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
				Security:    &SecurityHint{Public: true},
			}}
			le := executeCommandError(t, specs, "demo", "keys", "create")
			testutil.Require(t, le.Code == CodeUsage, "code = %q, want %q", le.Code, CodeUsage)
			testutil.Require(t, le.Detail == tc.wantDetail, "detail = %q, want %q", le.Detail, tc.wantDetail)
		})
	}
}

func TestBuild_UnsupportedOutputFormatDetail(t *testing.T) {
	specs := []CommandSpec{{
		Group:    "Usage",
		Use:      "summary",
		Method:   "GET",
		PathTpl:  "/usage",
		Security: &SecurityHint{Public: true},
	}}
	le := executeCommandError(t, specs, "-o", "bogus-format", "demo", "usage", "summary")
	want := "--output accepts: " + strings.Join(FormatterNames(), ", ")
	testutil.Require(t, le.Detail == want, "detail = %q, want %q", le.Detail, want)
	testutil.Require(t, !strings.Contains(le.Detail, "bogus-format"), "detail echoed user input: %q", le.Detail)
}

func TestBuild_APIErrorKnownErrorFallbackDetail(t *testing.T) {
	isolateRuntime(t)

	cases := []struct {
		name        string
		contentType string
		body        string
		known       []KnownError
		wantDetail  string
	}{
		{
			name:        "declared message wins over known error",
			contentType: "application/json",
			body:        `{"message":"key was revoked upstream"}`,
			known:       []KnownError{{Status: 403, Cause: "the API key is revoked"}},
			wantDetail:  "key was revoked upstream",
		},
		{
			name:        "known error fallback for non-json body",
			contentType: "text/plain",
			body:        "upstream-secret",
			known:       []KnownError{{Status: 403, Cause: "the API key is revoked"}},
			wantDetail:  "the API key is revoked",
		},
		{
			name:        "status mismatch keeps detail empty",
			contentType: "text/plain",
			body:        "upstream-secret",
			known:       []KnownError{{Status: 404, Cause: "no such key"}},
			wantDetail:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			specs := []CommandSpec{{
				Group:       "Keys",
				Use:         "reveal",
				Method:      "GET",
				PathTpl:     "/keys/reveal",
				KnownErrors: tc.known,
				Security:    &SecurityHint{Public: true},
			}}
			le := executeCommandError(t, specs, "--hostname", srv.URL, "demo", "keys", "reveal")
			testutil.Require(t, le.Code == CodeAPIError && le.Message == "API request failed", "error = %#v", le)
			testutil.Require(t, le.Detail == tc.wantDetail, "detail = %q, want %q", le.Detail, tc.wantDetail)
			testutil.Require(t, !strings.Contains(le.Detail, "upstream-secret"), "detail leaked raw body: %q", le.Detail)
		})
	}
}
