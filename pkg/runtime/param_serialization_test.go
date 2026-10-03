package runtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/config"
)

func TestParameterSerializationWire(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		params     []ParamSpec
		args       []string
		authCookie bool
		method     string
		escaped    string
		rawQuery   string
		header     string
		value      string
		cookie     string
	}{
		{
			name: "path simple scalar", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true}},
			args:   []string{"--id", "a b/c"}, method: "GET", escaped: "/items/a%20b%2Fc",
		},
		{
			name: "path simple array", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "[]string", Required: true}},
			args:   []string{"--id", "a,b"}, method: "GET", escaped: "/items/a,b",
		},
		{
			name: "label explode false", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "[]string", Required: true, Style: "label"}},
			args:   []string{"--id", "a,b"}, method: "GET", escaped: "/items/.a,b",
		},
		{
			name: "label explode true", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "[]string", Required: true, Style: "label", Explode: true}},
			args:   []string{"--id", "a,b"}, method: "GET", escaped: "/items/.a.b",
		},
		{
			name: "label scalar", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Style: "label"}},
			args:   []string{"--id", "a b/c"}, method: "GET", escaped: "/items/.a%20b%2Fc",
		},
		{
			name: "matrix explode false", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "[]string", Required: true, Style: "matrix"}},
			args:   []string{"--id", "a,b"}, method: "GET", escaped: "/items/;id=a,b",
		},
		{
			name: "matrix explode true", path: "/items/{id}",
			params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "[]string", Required: true, Style: "matrix", Explode: true}},
			args:   []string{"--id", "a,b"}, method: "GET", escaped: "/items/;id=a;id=b",
		},
		{
			name: "query default array", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "[]string"}},
			args:   []string{"--k", "a,b"}, method: "GET", escaped: "/items", rawQuery: "k=a&k=b",
		},
		{
			name: "form explode false", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "[]string", Style: "form"}},
			args:   []string{"--k", "a,b"}, method: "GET", escaped: "/items", rawQuery: "k=a,b",
		},
		{
			name: "form item comma", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "[]string", Style: "form"}},
			args:   []string{"--k", `"a,b",c`}, method: "GET", escaped: "/items", rawQuery: "k=a%2Cb,c",
		},
		{
			name: "spaceDelimited", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "[]string", Style: "spaceDelimited"}},
			args:   []string{"--k", "a,b"}, method: "GET", escaped: "/items", rawQuery: "k=a%20b",
		},
		{
			name: "pipeDelimited", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "[]string", Style: "pipeDelimited"}},
			args:   []string{"--k", "a,b"}, method: "GET", escaped: "/items", rawQuery: "k=a%7Cb",
		},
		{
			name: "spaceDelimited explode", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "[]string", Style: "spaceDelimited", Explode: true}},
			args:   []string{"--k", "a,b"}, method: "GET", escaped: "/items", rawQuery: "k=a&k=b",
		},
		{
			name: "allowReserved", path: "/items",
			params: []ParamSpec{{Name: "k", Flag: "k", In: InQuery, GoType: "string", AllowReserved: true}},
			args:   []string{"--k", "a/b:c?d#e&f=g+h"}, method: "GET", escaped: "/items", rawQuery: "k=a/b:c?d%23e%26f%3Dg%2Bh",
		},
		{
			name: "mixed key order", path: "/items",
			params: []ParamSpec{
				{Name: "b", Flag: "b", In: InQuery, GoType: "string"},
				{Name: "a", Flag: "a", In: InQuery, GoType: "string"},
			},
			args: []string{"--b", "2", "--a", "1"}, method: "GET", escaped: "/items", rawQuery: "a=1&b=2",
		},
		{
			name: "header scalar", path: "/items",
			params: []ParamSpec{{Name: "X-Trace", Flag: "x-trace", In: InHeader, GoType: "string"}},
			args:   []string{"--x-trace", "hello"}, method: "GET", escaped: "/items", header: "X-Trace", value: "hello",
		},
		{
			name: "two cookies", path: "/items",
			params: []ParamSpec{
				{Name: "a", Flag: "a", In: InCookie, GoType: "string"},
				{Name: "b", Flag: "b", In: InCookie, GoType: "string"},
			},
			args: []string{"--a", "1", "--b", "x y"}, method: "GET", escaped: "/items", cookie: "a=1; b=x%20y",
		},
		{
			name: "auth cookie then param", path: "/items", authCookie: true,
			params: []ParamSpec{{Name: "tenant", Flag: "tenant", In: InCookie, GoType: "string"}},
			args:   []string{"--tenant", "acme"}, method: "GET", escaped: "/items", cookie: "sid=s; tenant=acme",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, escaped, rawQuery, cookie, header := executeSerialized(t, CommandSpec{PathTpl: tc.path, Params: tc.params}, tc.args, tc.authCookie)
			testutil.Check(t, method == tc.method, "method = %q", method)
			testutil.Check(t, escaped == tc.escaped, "path = %q, want %q", escaped, tc.escaped)
			testutil.Check(t, rawQuery == tc.rawQuery, "query = %q, want %q", rawQuery, tc.rawQuery)
			testutil.Check(t, cookie == tc.cookie, "cookie = %q, want %q", cookie, tc.cookie)
			if tc.header != "" {
				testutil.Check(t, header.Get(tc.header) == tc.value, "header %s = %q, want %q", tc.header, header.Get(tc.header), tc.value)
			}
		})
	}
}

func executeSerialized(t *testing.T, spec CommandSpec, args []string, authCookie bool) (string, string, string, string, http.Header) {
	t.Helper()
	isolateRuntime(t)
	var method, escaped, rawQuery, cookie string
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		escaped = r.URL.EscapedPath()
		rawQuery = r.URL.RawQuery
		cookie = r.Header.Get("Cookie")
		header = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	if authCookie {
		hosts, err := config.LoadHosts()
		testutil.NoError(t, err)
		hosts.Set(srv.URL, config.HostEntry{AuthType: "apikey", APIKey: "sid=s", APIKeyHeader: "Cookie"})
		testutil.NoError(t, hosts.Save())
	}
	spec.Group = "Users"
	spec.Use = "list"
	spec.Method = "GET"
	spec.Security = &SecurityHint{Public: true}
	root := newExecutionRoot("raw")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{spec})
	root.SetArgs(append([]string{"demo", "users", "list", "--hostname", srv.URL}, args...))
	testutil.NoError(t, root.Execute())
	return method, escaped, rawQuery, cookie, header
}
