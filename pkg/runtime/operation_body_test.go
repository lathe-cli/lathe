package runtime

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_BodyFlagsAttachedWhenHasBody(t *testing.T) {
	specs := []CommandSpec{{
		Group:       "Users",
		Use:         "create-user",
		Method:      "POST",
		PathTpl:     "/users",
		RequestBody: &RequestBody{Required: true},
	}}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	users := mustFindChild(t, svc, "users")
	createUser := mustFindChild(t, users, "create-user")

	for _, name := range []string{"file", "set", "set-str"} {
		testutil.Check(t, createUser.Flag(name) != nil, "create-user missing --%s flag", name)
	}
}

func TestBuild_SetStrSendsStringBodyFields(t *testing.T) {
	specs := []CommandSpec{{
		Group:       "Users",
		Use:         "create-user",
		Method:      "POST",
		PathTpl:     "/users",
		RequestBody: &RequestBody{Required: true},
		Security:    &SecurityHint{Public: true},
	}}

	root, url, recorded := newRecordingRoot(t, specs[0])
	root.SetArgs([]string{
		"--hostname", url,
		"demo", "users", "create-user",
		"--set", "spec.replicas=3",
		"--set", "spec.enabled=true",
		"--set-str", "spec.stringReplicas=3",
		"--set-str", "spec.stringEnabled=true",
		"--set-str", "spec.csv=a,b",
	})

	testutil.NoError(t, root.Execute())

	rawBody, _ := recorded()
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(rawBody, &got))
	want := map[string]any{
		"spec": map[string]any{
			"replicas":       float64(3),
			"enabled":        true,
			"stringReplicas": "3",
			"stringEnabled":  "true",
			"csv":            "a,b",
		},
	}
	testutil.Check(t, reflect.DeepEqual(got, want), "body = %#v, want %#v", got, want)
}

func TestBuild_TypedBodyFlagsSendJSON(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "replace-limits",
		Method:  "PATCH",
		PathTpl: "/keys/{id}/limits",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", Argument: "key-id", In: InPath, GoType: "string", Required: true},
			{Name: "maxBudgetUsd", Flag: "max-budget-usd", In: InBody, GoType: "float64"},
			{Name: "budgetDuration", Flag: "budget-duration", In: InBody, GoType: "string", Enum: []string{"daily", "weekly", "monthly"}},
			{Name: "rpmLimit", Flag: "rpm-limit", In: InBody, GoType: "int64"},
			{Name: "allowedModels", Flag: "allowed-models", In: InBody, GoType: "[]string"},
			{Name: "expiresAt", Flag: "expires-at", In: InBody, GoType: "string"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
		Security:    &SecurityHint{Public: true},
	}}

	root, url, recorded := newRecordingRoot(t, specs[0])
	root.SetArgs([]string{
		"--hostname", url,
		"demo", "keys", "replace-limits", "key-1",
		"--max-budget-usd", "100",
		"--budget-duration", "monthly",
		"--rpm-limit", "60",
		"--allowed-models", "model-a,model-b",
	})
	testutil.NoError(t, root.Execute())
	rawBody, _ := recorded()
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(rawBody, &got))
	want := map[string]any{
		"maxBudgetUsd":   float64(100),
		"budgetDuration": "monthly",
		"rpmLimit":       float64(60),
		"allowedModels":  []any{"model-a", "model-b"},
	}
	testutil.Check(t, reflect.DeepEqual(got, want), "got %#v, want %#v", got, want)
}

func TestBuild_RequiredBodyFieldsAcceptEveryBodyInput(t *testing.T) {
	isolateRuntime(t)
	bodyFile := filepath.Join(t.TempDir(), "body.json")
	testutil.NoError(t, os.WriteFile(bodyFile, []byte(`{"name":"from-file"}`), 0o600))

	cases := []struct {
		name         string
		bodyRequired bool
		args         []string
		wantBody     string
	}{
		{name: "typed flag", bodyRequired: true, args: []string{"--name", "from-flag"}, wantBody: `{"name":"from-flag"}`},
		{name: "set", bodyRequired: true, args: []string{"--set", "name=from-set"}, wantBody: `{"name":"from-set"}`},
		{name: "file", bodyRequired: true, args: []string{"--file", bodyFile}, wantBody: `{"name":"from-file"}`},
		{name: "explicit null", bodyRequired: true, args: []string{"--set", "name=null"}, wantBody: `{"name":null}`},
		{name: "optional body omitted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			root := newExecutionRoot("raw")

			mustBuild(t, root, "demo", []CommandSpec{{
				Group:   "Users",
				Use:     "create-user",
				Method:  "POST",
				PathTpl: "/users",
				Params: []ParamSpec{{
					Name: "name", Flag: "name", In: InBody, GoType: "string", Required: true,
				}},
				RequestBody: &RequestBody{Required: tc.bodyRequired, MediaType: "application/json"},
				Security:    &SecurityHint{Public: true},
			}})
			args := []string{"--hostname", srv.URL, "demo", "users", "create-user"}
			root.SetArgs(append(args, tc.args...))
			testutil.NoError(t, root.Execute())
			testutil.Require(t, gotBody == tc.wantBody, "body = %q, want %q", gotBody, tc.wantBody)
		})
	}
}

func TestBuild_FileSendsRequestBodyMediaType(t *testing.T) {
	isolateRuntime(t)

	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	bodyFile := t.TempDir() + "/body.txt"
	testutil.NoError(t, os.WriteFile(bodyFile, []byte("hello"), 0600))

	root := newExecutionRoot("raw")

	mustBuild(t, root, "demo", []CommandSpec{{
		Group:       "Exports",
		Use:         "create-export",
		Method:      "POST",
		PathTpl:     "/exports",
		RequestBody: &RequestBody{Required: true, MediaType: "text/plain"},
		Security:    &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "exports", "create-export", "--file", bodyFile})

	testutil.NoError(t, root.Execute())
	testutil.Check(t, gotContentType == "text/plain", "Content-Type = %q, want text/plain", gotContentType)
}

func TestBuild_NonJSONRequestBodyRequiresFile(t *testing.T) {
	for _, args := range [][]string{
		{"--set", "id=1"},
		nil,
	} {
		isolateRuntime(t)

		root := newExecutionRoot("raw")

		mustBuild(t, root, "demo", []CommandSpec{{
			Group:       "Exports",
			Use:         "create-export",
			Method:      "POST",
			PathTpl:     "/exports",
			RequestBody: &RequestBody{Required: true, MediaType: "text/plain"},
		}})
		root.SetArgs(append([]string{"--hostname", "http://127.0.0.1:1", "demo", "exports", "create-export"}, args...))

		err := root.Execute()
		testutil.Require(t, err != nil && strings.Contains(err.Error(), "requires --file"), "Execute error = %v, want requires --file", err)
		if got := ClassifyError(err).Code; got != CodeUsage {
			t.Fatalf("error code = %q, want %q", got, CodeUsage)
		}
	}
}

func TestBuild_SetOnlyBodyFieldsHelpAndCoexistence(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "create",
		Method:  "POST",
		PathTpl: "/keys",
		Params: []ParamSpec{
			{Name: "name", Flag: "name", In: InBody, GoType: "string", Required: true, Help: "name (body, required)"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json", SetOnlyFields: []string{"limits"}},
		Security:    &SecurityHint{Public: true},
	}}
	root, url, recorded := newRecordingRoot(t, specs[0])

	cmd, _, err := root.Find([]string{"demo", "keys", "create"})
	testutil.Require(t, err == nil, "%v", err)
	for _, flag := range []string{"set", "set-str"} {
		usage := cmd.Flags().Lookup(flag).Usage
		testutil.Require(t, strings.Contains(usage, "limits"), "--%s help missing set-only field note: %q", flag, usage)
	}

	root.SetArgs([]string{
		"--hostname", url,
		"demo", "keys", "create",
		"--name", "demo",
		"--set", "limits.maxBudgetUsd=3",
	})
	testutil.NoError(t, root.Execute())
	rawBody, _ := recorded()
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(rawBody, &got))
	want := map[string]any{
		"name":   "demo",
		"limits": map[string]any{"maxBudgetUsd": float64(3)},
	}
	testutil.Require(t, reflect.DeepEqual(got, want), "body = %#v, want %#v", got, want)
}

func TestBuild_RequiredSetOnlyBodyFieldValidatedLocally(t *testing.T) {
	isolateRuntime(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Keys",
		Use:     "create",
		Method:  "POST",
		PathTpl: "/keys",
		Params: []ParamSpec{
			{Name: "name", Flag: "name", In: InBody, GoType: "string", Required: true, Help: "name (body, required)"},
		},
		RequestBody: &RequestBody{
			Required:      true,
			MediaType:     "application/json",
			Schema:        &SchemaSpec{Type: "object", Required: []string{"name", "limits"}, Properties: map[string]*SchemaSpec{"name": {Type: "string"}, "limits": {Type: "object"}}},
			SetOnlyFields: []string{"limits"},
		},
		Security: &SecurityHint{Public: true},
	}}
	root := newExecutionRoot("raw")

	root.SilenceErrors = true
	root.SilenceUsage = true
	mustBuild(t, root, "demo", specs)

	root.SetArgs([]string{"--hostname", srv.URL, "demo", "keys", "create", "--name", "demo"})
	err := root.Execute()
	var le *LatheError
	testutil.Require(t, errors.As(err, &le), "expected LatheError for missing required set-only field, got %v", err)
	testutil.Require(t, le.Code == CodeUsage && le.Detail == "missing required: limits", "error = %#v", le)

	root.SetArgs([]string{"--hostname", srv.URL, "demo", "keys", "create", "--name", "demo", "--set", "limits.rpm=3"})
	testutil.NoError(t, root.Execute())
}
