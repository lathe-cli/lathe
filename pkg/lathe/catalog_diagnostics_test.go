package lathe

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestCommandsShowDiagnosticIdentifierSafety(t *testing.T) {
	root := NewApp(testManifest())
	root.Use = "cli`\x1b]52;c;cHJvYmU=\a\u202e\n\tx"
	_, err := execute(root, "commands", "show", "missing-secret")
	le := runtime.ClassifyError(err)
	const clean = "cli' ]52;c;cHJvYmU= x"
	want := "no generated command at that path; run `" + clean + " search \"<intent>\"` or `" + clean + " commands` to list paths"
	if le.Detail != want {
		t.Fatalf("detail = %q, want %q", le.Detail, want)
	}
	for _, value := range []string{le.Detail, le.Hint} {
		if strings.Contains(value, "missing-secret") || strings.Contains(value, "%!(") {
			t.Errorf("unsafe diagnostic: %q", value)
		}
		for _, r := range value {
			if !unicode.IsGraphic(r) {
				t.Errorf("non-graphic diagnostic: %q", value)
				break
			}
		}
	}
}

func TestHiddenBodyHintCatalogInspection(t *testing.T) {
	for _, visibility := range []string{"visible", "leaf", "ancestor", "root"} {
		t.Run(visibility, func(t *testing.T) {
			root := NewApp(testManifest())
			if err := runtime.BuildFlat(root, "demo", []runtime.CommandSpec{
				{Group: "Items", Use: "create", Hidden: visibility == "leaf", Method: "POST", RequestBody: &runtime.RequestBody{MediaType: "application/json", Schema: &runtime.SchemaSpec{Type: "object", Properties: map[string]*runtime.SchemaSpec{"tags": {Type: "array"}}}}},
				{Group: "Items", Use: "list", Method: "GET"},
			}); err != nil {
				t.Fatal(err)
			}
			leaf, _, err := root.Find([]string{"items", "create"})
			if err != nil {
				t.Fatal(err)
			}
			if visibility == "ancestor" {
				leaf.Parent().Hidden = true
			}
			root.Hidden = visibility == "root"
			_, err = execute(root, "items", "create", "--set", `tags=["secret-val"]`)
			if err == nil {
				t.Fatal("invalid body accepted")
			}
			le := runtime.ClassifyError(err)
			prefix := "myctl commands show "
			if visibility == "leaf" || visibility == "ancestor" {
				prefix += "--include-hidden "
			}
			want := "run `" + prefix + "items create --json` to inspect body.schema, then correct --set, --set-str, or --file"
			if le.Hint != want {
				t.Fatalf("hint = %q, want %q", le.Hint, want)
			}
			args := strings.Fields(strings.Split(le.Hint, "`")[1])[1:]
			out, err := execute(root, args...)
			if err != nil {
				t.Fatalf("hint command failed: %v", err)
			}
			var entry runtime.CatalogCommand
			if err := json.Unmarshal([]byte(out), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Body == nil || entry.Body.Schema == nil || entry.Body.Schema.Properties["tags"].Type != "array" {
				t.Fatalf("hint did not inspect body.schema: %s", out)
			}
		})
	}
}
