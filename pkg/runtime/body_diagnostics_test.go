package runtime

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func TestBodyDiagnosticsContract(t *testing.T) {
	object := &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{
		"tags":     {Type: "array", Items: &SchemaSpec{Type: "string"}},
		"override": {Type: "object", Nullable: true},
		"items":    {Type: "array", Items: &SchemaSpec{Type: "object", Required: []string{"b", "a"}}},
		"name":     {Type: "string"}, "count": {Type: "integer"}, "enabled": {Type: "boolean"},
	}}
	for _, tc := range []struct {
		name      string
		schema    *SchemaSpec
		args      []string
		body      string
		params    []ParamSpec
		media     string
		template  string
		mergePath string
		required  bool
		want      []string
	}{
		{name: "array string", schema: object, args: []string{"--set", `tags=["secret-val"]`}, want: []string{"#/tags must be array", "--set tags[0]=", "--set tags=[]", "--file"}},
		{name: "nullable object string", schema: object, args: []string{"--set", `override={"secret-key":"secret-val"}`}, want: []string{"#/override must be object or null", "--set override.<field>=", "--file"}},
		{name: "array object", schema: object, args: []string{"--set", "items[0]=secret-val"}, want: []string{"#/items/0 must be object", "--set items[0].<field>="}},
		{name: "string inference", schema: object, args: []string{"--set", "name=[]"}, want: []string{"#/name must be string", "--set-str"}},
		{name: "integer string", schema: object, args: []string{"--set-str", "count=secret-val"}, want: []string{"#/count must be integer", "--set", "--set-str", "--file"}},
		{name: "boolean string", schema: object, args: []string{"--set-str", "enabled=secret-val"}, want: []string{"#/enabled must be boolean", "--set"}},
		{name: "missing equals", schema: object, args: []string{"--set", "secret-raw-token"}, want: []string{"--set expects path=value", "--file"}},
		{name: "empty key", schema: object, args: []string{"--set-str", "=secret-val"}, want: []string{"--set-str", "non-empty field path"}},
		{name: "path conflict", schema: object, args: []string{"--set", "secret-key=secret-val", "--set-str", "secret-key.child=secret-val"}, want: []string{"conflicting --set/--set-str paths"}},
		{name: "array conflict", schema: object, args: []string{"--set", "secret-key=secret-val", "--set", "secret-key[0]=secret-val"}, want: []string{"conflicting --set/--set-str paths"}},
		{name: "array item conflict", schema: object, args: []string{"--set", "secret-key[0]=secret-val", "--set", "secret-key[0].child=secret-val"}, want: []string{"conflicting --set/--set-str paths"}},
		{name: "root array", schema: &SchemaSpec{Type: "array"}, body: `"secret-val"`, want: []string{"#/ must be array", "--file"}},
		{name: "null array object", schema: &SchemaSpec{Type: "array", Items: &SchemaSpec{Type: "object"}}, body: `[null]`, want: []string{"#/0 must be object", "--file"}},
		{name: "missing array fields", schema: &SchemaSpec{Type: "array", Items: &SchemaSpec{Type: "object", Required: []string{"b", "a"}}}, body: `[{}]`, want: []string{"missing required: #/0/a, #/0/b"}},
		{name: "file set conflict", schema: object, params: []ParamSpec{{Name: "name", Flag: "name", In: InBody, GoType: "string"}}, body: `{}`, args: []string{"--set", "name=secret-val"}, want: []string{"--file cannot be combined with --set"}},
		{name: "body flag conflict", schema: object, params: []ParamSpec{{Name: "name", Flag: "name", In: InBody, GoType: "string"}}, args: []string{"--name", "secret-val", "--set", "name=secret-val"}, want: []string{`body field "name"`, "--name", "--set"}},
		{name: "body flag named set", schema: object, params: []ParamSpec{{Name: "set", Flag: "set", In: InBody, GoType: "string"}}, args: []string{"--set", "secret-val", "--lathe-set", "set=secret-val"}, want: []string{`body field "set" cannot be set by both --set and --lathe-set`}},
		{name: "non JSON set", media: "text/plain", args: []string{"--set", "secret-key=secret-val"}, want: []string{"text/plain requires --file"}},
		{name: "non JSON body flag", media: "text/plain", params: []ParamSpec{{Name: "name", Flag: "name", In: InBody, GoType: "string"}}, args: []string{"--name", "secret-val"}, want: []string{"text/plain requires --file"}},
		{name: "non JSON required", media: "text/plain", required: true, want: []string{"text/plain requires --file"}},
		{name: "envelope invalid JSON", schema: object, template: `{"variables":{}}`, mergePath: "variables", body: `{"secret-key":"secret-val`, want: []string{"request body must contain one valid JSON value"}},
		{name: "envelope no merge path", schema: object, template: `{"query":"query"}`, body: `{"secret-key":"secret-val"}`, want: []string{"--file is not supported"}},
		{name: "renamed controls", schema: object, params: []ParamSpec{{Name: "set", Flag: "set", In: InQuery, GoType: "string"}, {Name: "set-str", Flag: "set-str", In: InQuery, GoType: "string"}, {Name: "file", Flag: "file", In: InQuery, GoType: "string"}}, args: []string{"--lathe-set", `tags=["secret-val"]`}, want: []string{"--lathe-set tags[0]=", "--lathe-set tags=[]", "--lathe-file"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newWorkflowRoot(&strings.Builder{})
			commands := &cobra.Command{Use: "commands"}
			commands.AddCommand(&cobra.Command{Use: "show"})
			root.AddCommand(commands)
			if tc.media == "" {
				tc.media = "application/json"
			}
			spec := CommandSpec{Group: "Items", Use: "create", Method: "POST", Params: tc.params, RequestBody: &RequestBody{MediaType: tc.media, Schema: tc.schema, Template: tc.template, MergePath: tc.mergePath, Required: tc.required}}
			if err := Build(root, "demo", []CommandSpec{spec}); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"demo", "items", "create"}, tc.args...)
			if tc.body != "" {
				file := t.TempDir() + "/body.json"
				if err := os.WriteFile(file, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--file", file)
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("invalid body accepted")
			}
			le := ClassifyError(err)
			for _, want := range tc.want {
				if !strings.Contains(le.Detail, want) {
					t.Errorf("detail %q missing %q", le.Detail, want)
				}
			}
			if strings.Contains(le.Detail, "must be") && !strings.Contains(le.Hint, root.Name()+" commands show demo items create --json") {
				t.Errorf("schema hint does not inspect exact command: %q", le.Hint)
			}
			if utf8.RuneCountInString(le.Detail) > 240 || strings.ContainsAny(le.Detail, "\r\n") {
				t.Fatalf("unbounded detail: %q", le.Detail)
			}
			for _, format := range []string{"json", "table"} {
				var out strings.Builder
				if FormatError(err, format, &out) != ExitUsage {
					t.Fatalf("expected exit 2: %s", out.String())
				}
				for _, secret := range []string{"secret-val", "secret-raw-token", "secret-key", "got string", "got null"} {
					if strings.Contains(out.String(), secret) {
						t.Fatalf("input leaked in %s: %s", format, out.String())
					}
				}
			}
		})
	}
}

func TestMultipartBodyDiagnosticsContract(t *testing.T) {
	for _, input := range []OperationInput{{HasFile: true, FileBody: []byte("secret-val")}, {BodySets: []string{"secret-key=secret-val"}}, {BodyStringSets: []string{"secret-key=secret-val"}}} {
		_, err := resolveOperationBody(CommandSpec{RequestBody: &RequestBody{MediaType: "multipart/form-data"}}, input, nil, nil, nil)
		le := ClassifyError(err)
		if le == nil || le.Message != "multipart request bodies accept only part flags" || !strings.Contains(le.Detail, "part flags") {
			t.Fatalf("multipart guidance missing: %#v", le)
		}
		var out strings.Builder
		if FormatError(err, "json", &out) != ExitUsage || strings.Contains(out.String(), "secret-") {
			t.Fatalf("unsafe multipart error: %s", out.String())
		}
	}
}
