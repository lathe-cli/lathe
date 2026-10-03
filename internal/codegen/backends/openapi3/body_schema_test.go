package openapi3

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestRequestBodyInputSemantics(t *testing.T) {
	for _, document := range []string{
		`{"type":"object","required":["id","name"],"properties":{"id":{"$ref":"#/components/schemas/A"},"name":{"type":"string"}}}`,
		`{"type":"object","required":["id","name"],"properties":{"id":{"anyOf":[{"type":"integer"},{"type":"null"}],"readOnly":true},"name":{"type":"string"}}}`,
		`{"type":"object","required":["id","name"],"properties":{"id":{"type":"integer","readOnly":true},"name":{"type":"string"}}}`,
		`{"type":"object","required":["id","name"],"properties":{"name":{"type":"string"}},"allOf":[{"properties":{"id":{"type":"integer","readOnly":true}}}]}`,
		`{"type":"object","allOf":[{"required":["id","name"]},{"properties":{"id":{"type":"integer","readOnly":true},"name":{"type":"string"}}}]}`,
		`{"$ref":"#/components/schemas/Pet","required":["id"]}`,
	} {
		t.Run(document, func(t *testing.T) {
			var node schemaNode
			testutil.NoError(t, json.Unmarshal([]byte(document), &node))
			schema := convertSchema(&node)
			pet := convertSchema(&schemaNode{Type: schemaType{Value: "object"}, Required: []string{"name"}, Properties: map[string]*schemaNode{"id": {Type: schemaType{Value: "integer"}, ReadOnly: true}, "name": {Type: schemaType{Value: "string"}}}})
			mod := &rawir.RawModule{Name: "demo", Schemas: map[string]*rawir.RawSchema{"Pet": pet, "A": {Ref: rawir.RefPrefix + "B"}, "B": {Type: "integer", ReadOnly: true}}, Operations: []rawir.RawOperation{{OperationID: "CreatePet", Method: "POST", Path: "/pets", RequestBody: &rawir.RawRequestBody{Schema: schema}}}}
			spec := normalize.Normalize(mod)[0]
			_, err := runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{HasFile: true, FileBody: []byte(`{"name":"rex"}`)}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
			testutil.NoError(t, err)
			_, err = runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{HasFile: true, FileBody: []byte(`{}`)}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
			testutil.Require(t, err != nil, "writable required field not enforced")
		})
	}
}
