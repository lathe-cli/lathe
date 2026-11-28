package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestOperationLocationIdentity(t *testing.T) {
	for _, location := range []string{InHeader, InPath, InFormData} {
		t.Run(location, func(t *testing.T) {
			var calls int
			wantQuery, wantOther := "query-value", "other-value"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("id") != wantQuery {
					t.Errorf("query = %s", r.URL.RawQuery)
				}
				switch location {
				case InHeader:
					if r.Header.Get("id") != wantOther {
						t.Errorf("header = %s", r.Header.Get("id"))
					}
				case InPath:
					if r.URL.Path != "/"+wantOther {
						t.Errorf("path = %s", r.URL.Path)
					}
				case InFormData:
					if wantOther != "" {
						if err := r.ParseMultipartForm(1024); err != nil {
							t.Error(err)
						}
					}
					if r.PostFormValue("id") != wantOther {
						t.Errorf("form = %v", r.PostForm)
					}
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			spec := CommandSpec{Method: "POST", PathTpl: "/", Security: &SecurityHint{Public: true}, Params: []ParamSpec{
				{Name: "id", Flag: "query-id", In: InQuery, GoType: "string"},
				{Name: "id", Flag: "other-id", In: location, GoType: "string"},
			}}
			if location == InPath {
				spec.PathTpl = "/{id}"
			}
			if location == InFormData {
				spec.RequestBody = &RequestBody{MediaType: "multipart/form-data"}
			}
			_, err := InvokeOperation(context.Background(), spec, OperationInput{Values: map[string]any{"query:id": "query-value", location + ":id": "other-value"}}, OperationOptions{Hostname: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("requests = %d", calls)
			}
			_, err = InvokeOperation(context.Background(), spec, OperationInput{Values: map[string]any{"id": "unsafe"}}, OperationOptions{Hostname: server.URL})
			if err == nil {
				t.Fatal("ambiguous input accepted")
			}
			if calls != 1 {
				t.Fatal("ambiguous input reached HTTP")
			}
			for _, values := range []map[string]any{
				{"query-id": "query-value", "other-id": "other-value"},
				{boundParamKey(spec.Params[0]): "query-value", boundParamKey(spec.Params[1]): "other-value"},
			} {
				if _, err := InvokeOperation(context.Background(), spec, OperationInput{Values: values}, OperationOptions{Hostname: server.URL}); err != nil {
					t.Fatal(err)
				}
			}
			if location != InPath {
				wantOther = ""
				if _, err := InvokeOperation(context.Background(), spec, OperationInput{Values: map[string]any{"query:id": wantQuery}}, OperationOptions{Hostname: server.URL}); err != nil {
					t.Fatal(err)
				}
			}
			before := calls
			if _, err := InvokeOperation(context.Background(), spec, OperationInput{Changed: map[string]bool{"id": true}}, OperationOptions{Hostname: server.URL}); err == nil || calls != before {
				t.Fatal("ambiguous changed key accepted")
			}
		})
	}
}

func TestSensitiveBodyAssignments(t *testing.T) {
	schema := &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{
		"settings": {Type: "object", Properties: map[string]*SchemaSpec{
			"label": {Type: "string"}, "pin": {Type: "string", Format: "password"}, "password": {Type: "string", Format: "password"},
		}},
		"entries":  {Type: "array", Items: &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{"api_key": {Type: "string"}}}},
		"custom":   {Type: "string", Format: "password"},
		"variants": {AnyOf: []*SchemaSpec{{Type: "array", Items: &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{"pin": {Format: "password"}}}}}},
	}}
	for _, template := range []bool{false, true} {
		for _, stringSet := range []bool{false, true} {
			for _, path := range []string{"settings.password", "settings.pin", "entries[0].api_key", "variants[0].pin", "custom", "token"} {
				spec := CommandSpec{Method: "POST", PathTpl: "/", Security: &SecurityHint{Public: true}, RequestBody: &RequestBody{Schema: schema}, Params: []ParamSpec{{Name: "token", Flag: "token", In: InBody, GoType: "string"}}}
				if template {
					spec.RequestBody.Template = `{"query":"mutation"}`
					spec.RequestBody.MergePath = "variables"
				}
				input := OperationInput{BodySets: []string{path + "=DO-NOT-ECHO"}}
				if stringSet {
					input.BodyStringSets = input.BodySets
					input.BodySets = nil
				}
				_, err := InvokeOperation(context.Background(), spec, input, OperationOptions{Hostname: "https://example.invalid", DryRun: true})
				var usage *LatheError
				if !errors.As(err, &usage) || usage.Code != CodeUsage || usage.ExitCode != ExitUsage {
					t.Fatalf("path %s template %v string %v: error = %v", path, template, stringSet, err)
				}
				if strings.Contains(err.Error(), "DO-NOT-ECHO") || !strings.Contains(err.Error(), path) {
					t.Fatalf("unsafe error = %v", err)
				}
			}
		}
	}
	spec := CommandSpec{Method: "POST", PathTpl: "/", Security: &SecurityHint{Public: true}, RequestBody: &RequestBody{Schema: schema, SetOnlyFields: []string{"settings"}}}
	for _, input := range []OperationInput{{BodySets: []string{"settings.label=demo"}}, {FileBody: []byte(`{"settings":{"password":"from-file"}}`), HasFile: true}} {
		if _, err := InvokeOperation(context.Background(), spec, input, OperationOptions{Hostname: "https://example.invalid", DryRun: true}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHiddenParameterDiscoveryAndExecution(t *testing.T) {
	var param ParamSpec
	if err := json.Unmarshal([]byte(`{"Name":"legacy","Flag":"legacy","In":"query","GoType":"string","Hidden":true}`), &param); err != nil {
		t.Fatal(err)
	}
	spec := CommandSpec{Group: "items", Use: "get", Method: "GET", PathTpl: "/", Params: []ParamSpec{param}, Security: &SecurityHint{Public: true}}
	root := &cobra.Command{Use: "cli"}
	if err := Build(root, "api", []CommandSpec{spec}); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := root.Find([]string{"api", "items", "get"})
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.Flags().Lookup("legacy").Hidden {
		t.Fatal("hidden flag appears in help")
	}
	if err := cmd.ParseFlags([]string{"--legacy", "value"}); err != nil {
		t.Fatal(err)
	}
	detail, ok := FindCatalogCommand(root, []string{"api", "items", "get"}, CatalogOptions{})
	if !ok || len(detail.Flags) != 0 {
		t.Fatalf("default detail = %+v", detail)
	}
	catalog := BuildCatalog(root, CatalogOptions{})
	if len(catalog.Commands) != 1 || len(catalog.Commands[0].Flags) != 0 {
		t.Fatalf("default catalog = %+v", catalog.Commands)
	}
	catalog = BuildCatalog(root, CatalogOptions{IncludeHidden: true})
	if len(catalog.Commands[0].Flags) != 1 {
		t.Fatal("hidden flag missing from full catalog")
	}
	if catalog.Commands[0].Flags[0].Key != "query:legacy" || !catalog.Commands[0].Flags[0].Hidden {
		t.Fatalf("flag = %+v", catalog.Commands[0].Flags[0])
	}
}

func TestQualifiedBindings(t *testing.T) {
	params := []ParamSpec{{Name: "id", Flag: "query-id", In: InQuery, GoType: "string"}, {Name: "id", Flag: "header-id", In: InHeader, GoType: "string"}}
	spec := CommandSpec{Use: "get", Params: params}
	for _, key := range []string{"query:id", "query-id"} {
		target, err := shortcutSpec(spec, CommandShortcut{Use: "quick", Params: map[string]string{key: "value"}})
		if err != nil || target.Params[0].Default != "value" || target.Params[1].Default != "" {
			t.Fatalf("shortcut %s = %+v %v", key, target, err)
		}
		_, input, err := runtimeSchemaInput(spec, OperationInput{Values: map[string]any{"header:id": "value"}}, RuntimeSchemaSpec{Operation: spec, Params: map[string]string{key: "${params.header:id}"}})
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := operationValue(input, params[0])
		if err != nil || !present || value != "value" {
			t.Fatalf("schema input = %+v %v", input, err)
		}
	}
	if _, err := shortcutSpec(spec, CommandShortcut{Use: "quick", Params: map[string]string{"id": "value"}}); err == nil {
		t.Fatal("ambiguous shortcut accepted")
	}
}

func TestPreferredColumnsIsolation(t *testing.T) {
	first := PreferredColumns()
	original := first[0]
	first[0] = "changed"
	if PreferredColumns()[0] != original {
		t.Fatal("caller changed shared columns")
	}
}

func TestQualifiedWireNamesDoNotLeak(t *testing.T) {
	wantHeader, wantQuery := "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantHeader || r.URL.Query().Get("header:Authorization") != wantQuery {
			t.Errorf("parameter crossed locations: header=%q query=%q", r.Header.Get("Authorization"), r.URL.Query().Get("header:Authorization"))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	spec := CommandSpec{Method: "GET", PathTpl: "/", Security: &SecurityHint{Public: true}, Params: []ParamSpec{
		{Name: "header:Authorization", Flag: "query-value", In: InQuery, GoType: "string"},
		{Name: "Authorization", Flag: "header-value", In: InHeader, GoType: "string"},
	}}
	for _, key := range []string{"header:Authorization", "query:header:Authorization"} {
		wantHeader, wantQuery = "", ""
		if key == "header:Authorization" {
			wantHeader = "marker"
		} else {
			wantQuery = "marker"
		}
		if _, err := InvokeOperation(context.Background(), spec, OperationInput{Values: map[string]any{key: "marker"}}, OperationOptions{Hostname: server.URL}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSensitiveEnvelopeVariableAssignment(t *testing.T) {
	spec := CommandSpec{Method: "POST", PathTpl: "/graphql", Security: &SecurityHint{Public: true}, Params: []ParamSpec{{Name: "pin", Flag: "password", In: InVariable, GoType: "string"}}, RequestBody: &RequestBody{Template: `{"query":"mutation"}`, MergePath: "variables", Schema: &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{"variables": {Type: "object", Properties: map[string]*SchemaSpec{"pin": {Type: "string"}}}}}}}
	_, err := InvokeOperation(context.Background(), spec, OperationInput{BodyStringSets: []string{"pin=DO-NOT-ECHO"}}, OperationOptions{Hostname: "https://example.invalid", DryRun: true})
	var usage *LatheError
	if !errors.As(err, &usage) || usage.Code != CodeUsage || strings.Contains(err.Error(), "DO-NOT-ECHO") {
		t.Fatalf("sensitive envelope input = %v", err)
	}
}
