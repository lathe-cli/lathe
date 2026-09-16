package render

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestGoLiteralCompilesAndPreservesSpecs(t *testing.T) {
	param := runtime.ParamSpec{Name: "name", Flag: "name", Aliases: []string{"n"}, Argument: "name", In: "body", GoType: "string", Help: "say \"hello\"\n世界\t`\\", Required: true, Default: "default", Enum: []string{"one"}, ItemEnum: []string{"two"}, Format: "password", Deprecated: true, Context: "workspace"}
	scalar := &runtime.SchemaSpec{Type: "string", Enum: []string{"value"}}
	spec := runtime.CommandSpec{
		Group: "items", GroupShort: "Item operations", Use: "create", Aliases: []string{"add"},
		Shortcuts: []runtime.CommandShortcut{{Use: "quick", Params: map[string]string{"name": "one"}}},
		Short:     "Create item", Long: "Long description", Example: "cli items create", OperationID: "createItem", Hidden: true, Deprecated: true,
		Method: "POST", PathTpl: "/items", DefaultHostname: "example.test", Params: []runtime.ParamSpec{param},
		Examples: []runtime.CommandExample{{Summary: "Create", Command: "cli create", BodyShape: json.RawMessage(`{"name":"a\nb"}`), OutputHints: &runtime.ExampleOutputHints{IDPath: "id", ListPath: "items"}, FollowUpCommands: []string{"cli list"}}},
		RequestBody: &runtime.RequestBody{Required: true, MediaType: "application/json", Template: `{"variables":{}}`, MergePath: "variables", SetOnlyFields: []string{"nested"},
			Schema: &runtime.SchemaSpec{Ref: "#/Thing", Type: "object", Description: "Thing", Format: "custom", Nullable: true, Enum: []string{"x"},
				Properties: map[string]*runtime.SchemaSpec{"z": scalar, "a": scalar}, Required: []string{"z"}, Items: scalar,
				AnyOf: []*runtime.SchemaSpec{scalar}, OneOf: []*runtime.SchemaSpec{scalar}, AllOf: []*runtime.SchemaSpec{scalar},
				AdditionalProperties: &runtime.AdditionalPropertiesSpec{Allowed: true, Schema: scalar}},
			RuntimeSchema: &runtime.RuntimeSchemaSpec{Operation: runtime.CommandSpec{Use: "schema", Security: &runtime.SecurityHint{}}, ResponsePath: "schema", Params: map[string]string{"name": "${params.name}"}}},
		Output: runtime.OutputHints{ListPath: "items", DefaultColumns: []string{"name"}, ColumnLabels: map[string]string{"name": "Name"},
			ColumnFormats: map[string]runtime.ColumnFormat{"cost": {Kind: "currency", Currency: "USD", SourceScale: 6, Grouping: true, MinFractionDigits: 2, MaxFractionDigits: 6}}, ColumnAlignments: map[string]string{"cost": "right"}, ResponseMediaType: "application/json",
			Pagination: &runtime.PaginationHint{Strategy: "cursor", TokenParam: "after", TokenField: "next", LimitParam: "limit"},
			Streaming: &runtime.StreamingHint{Strategy: "sse", Policy: &runtime.StreamPolicy{DataFormat: "json", EventNamePath: "type",
				Collect: &runtime.StreamCollectHint{RequireStop: true, StopEvents: []string{"done"}, PauseEvents: []string{"pause"}, ErrorEvents: []string{"error"}, Fields: []runtime.StreamFieldRule{{Events: []string{"data"}, From: "text", Value: "literal", To: "result", Reduce: "concat"}}},
				Live:    &runtime.StreamLiveHint{Events: []string{"data"}, From: "text"}}}},
		Security: &runtime.SecurityHint{Public: true, Scopes: []string{"write"}}, Notes: []string{"note"}, Prerequisites: []string{"login"}, KnownErrors: []runtime.KnownError{{Status: 409, Cause: "exists"}}, SetContext: &runtime.ContextSetHint{Name: "workspace", Param: "name"}, Mutation: "write", SearchTerms: []string{"add"},
	}
	workflow := runtime.WorkflowSpec{Use: "deploy", Aliases: []string{"up"}, Short: "Deploy", Long: "Deploy all", Example: "cli deploy", Hidden: true, Deprecated: true, Params: []runtime.ParamSpec{param}, OutputFrom: "create", Output: spec.Output,
		Steps: []runtime.WorkflowStepSpec{{ID: "create", Operation: spec, When: []runtime.WorkflowCondition{{Value: "${input.name}", Operator: "in", Values: []string{"one", ""}}}, Params: map[string]string{"name": "${input.name}"}, BodySets: []runtime.WorkflowValue{{Name: "count", Value: "1"}}, BodyStringSets: []runtime.WorkflowValue{{Name: "name", Value: "one"}}}}}
	type specs struct {
		Commands  []runtime.CommandSpec
		Workflows []runtime.WorkflowSpec
	}
	want := specs{[]runtime.CommandSpec{spec, {}}, []runtime.WorkflowSpec{workflow}}
	commands, err := goLiteral(want.Commands)
	testutil.Require(t, err == nil, "%v", err)
	workflows, err := goLiteral(want.Workflows)
	testutil.Require(t, err == nil, "%v", err)
	again, err := goLiteral(want.Commands)
	testutil.Require(t, err == nil && commands == again, "literal is not deterministic: %v", err)
	edge, err := goLiteral(&runtime.SchemaSpec{Properties: map[string]*runtime.SchemaSpec{"nil": nil}, AnyOf: []*runtime.SchemaSpec{nil}, AdditionalProperties: &runtime.AdditionalPropertiesSpec{}})
	testutil.Require(t, err == nil, "%v", err)
	empty, err := goLiteral(runtime.CommandSpec{Aliases: []string{}, Examples: []runtime.CommandExample{}, Output: runtime.OutputHints{ColumnLabels: map[string]string{}}})
	testutil.Require(t, err == nil, "%v", err)
	source := fmt.Sprintf(`package main
import ("encoding/gob"; "os"; "reflect"; "github.com/lathe-cli/lathe/pkg/runtime")
func main() {
    value := struct { Commands []runtime.CommandSpec; Workflows []runtime.WorkflowSpec }{%s, %s}
    edge := %s
    if len(edge.Properties) != 1 || edge.Properties["nil"] != nil || len(edge.AnyOf) != 1 || edge.AnyOf[0] != nil || edge.AdditionalProperties == nil || edge.AdditionalProperties.Allowed { panic("nil pointer values changed") }
    if !reflect.DeepEqual(%s, runtime.CommandSpec{}) { panic("empty field normalization changed") }
    if err := gob.NewEncoder(os.Stdout).Encode(value); err != nil { panic(err) }
}
`, commands, workflows, edge, empty)
	file := filepath.Join(t.TempDir(), "main.go")
	testutil.NoError(t, os.WriteFile(file, []byte(source), 0o600))
	cmd := exec.Command("go", "run", file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	testutil.Require(t, err == nil, "compile and run generated literals: %v\n%s", err, &stderr)
	var got specs
	testutil.NoError(t, gob.NewDecoder(bytes.NewReader(output)).Decode(&got))
	testutil.Require(t, reflect.DeepEqual(got, want), "compiled declarations changed values\ngot: %#v\nwant: %#v", got, want)
}
