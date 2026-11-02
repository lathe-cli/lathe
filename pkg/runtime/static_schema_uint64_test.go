package runtime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestStaticBodySchemaUint64SetPrecision(t *testing.T) {
	for _, typ := range []string{"string", "integer"} {
		for _, mode := range []string{"body", "template", "body-flags"} {
			for _, value := range []string{"9223372036854775808", "9223372036854775809", "18446744073709551615"} {
				t.Run(typ+"/"+mode+"/"+value, func(t *testing.T) {
					field := &SchemaSpec{Type: typ, Format: "uint64"}
					spec := CommandSpec{RequestBody: &RequestBody{
						MediaType: "application/json",
						Schema: &SchemaSpec{Type: "object", Properties: map[string]*SchemaSpec{
							"v":      field,
							"nested": {Type: "object", Properties: map[string]*SchemaSpec{"v": field}},
							"items":  {Type: "array", Items: field},
							"map":    {Type: "object"},
						}},
					}}
					input := OperationInput{BodySets: []string{
						"v=" + value,
						"nested.v=" + value,
						"items[0]=" + value,
						"map.key=" + value,
					}}
					want := fmt.Sprintf(`{"items":[%s],"map":{"key":%s},"nested":{"v":%s},"v":%s}`, value, value, value, value)
					switch mode {
					case "template":
						spec.RequestBody.Template = `{"query":"query", "variables":{}}`
						spec.RequestBody.MergePath = "variables"
						want = `{"query":"query","variables":` + want + `}`
					case "body-flags":
						spec.Params = []ParamSpec{{Name: "enabled", Flag: "enabled", In: InBody, GoType: "bool"}}
						input.Values = map[string]any{"enabled": true}
						want = `{"enabled":true,` + want[1:]
					}
					_, body, _, err := resolveOperationRequest(spec, input, ClientOptions{})
					testutil.NoError(t, err)
					raw, _, err := encodeRequestBody(body)
					testutil.NoError(t, err)
					testutil.Require(t, strings.TrimSpace(string(raw)) == want, "body = %s, want %s", raw, want)
				})
			}
		}
	}
}
