package swagger

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
	var node schemaNode
	testutil.NoError(t, json.Unmarshal([]byte(`{"type":"object","properties":{"name":{"type":"string","x-nullable":true}}}`), &node))
	schema := convertSchema(&node)
	mod := &rawir.RawModule{Name: "demo", Operations: []rawir.RawOperation{{OperationID: "CreatePet", Method: "POST", Path: "/pets", RequestBody: &rawir.RawRequestBody{Schema: schema}}}}
	spec := normalize.Normalize(mod)[0]
	_, err := runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{HasFile: true, FileBody: []byte(`{"name":null}`)}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
	testutil.NoError(t, err)
	_, err = runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{HasFile: true, FileBody: []byte(`{"name":7}`)}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
	testutil.Require(t, err != nil, "invalid input accepted")
}
