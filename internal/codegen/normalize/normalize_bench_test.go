package normalize

import (
	"fmt"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
)

// benchModule builds a raw IR module with the shapes normalization actually
// has to resolve: shared `$ref` schemas, list envelopes with pagination
// hints, request bodies, and mixed parameter locations.
func benchModule(resources int) *rawir.RawModule {
	schemas := map[string]*rawir.RawSchema{
		"Owner": {
			Type: "object",
			Properties: map[string]*rawir.RawSchema{
				"id":    {Type: "string"},
				"email": {Type: "string", Format: "email"},
			},
		},
	}
	operations := make([]rawir.RawOperation, 0, resources*4)

	for i := range resources {
		name := fmt.Sprintf("Resource%d", i)
		collection := fmt.Sprintf("/v1/resource%ds", i)

		schemas[name] = &rawir.RawSchema{
			Type: "object",
			Properties: map[string]*rawir.RawSchema{
				"id":          {Type: "string"},
				"name":        {Type: "string"},
				"description": {Type: "string"},
				"created_at":  {Type: "string", Format: "date-time"},
				"enabled":     {Type: "boolean"},
				"size":        {Type: "integer", Format: "int64"},
				"tags":        {Type: "array", Items: &rawir.RawSchema{Type: "string"}},
				"owner":       {Ref: rawir.RefPrefix + "Owner"},
			},
			Required: []string{"name"},
		}
		schemas[name+"List"] = &rawir.RawSchema{
			Type: "object",
			Properties: map[string]*rawir.RawSchema{
				"items":           {Type: "array", Items: &rawir.RawSchema{Ref: rawir.RefPrefix + name}},
				"next_page_token": {Type: "string"},
			},
		}

		itemResponse := map[string]*rawir.RawResponse{
			"200": {MediaType: "application/json", Schema: &rawir.RawSchema{Ref: rawir.RefPrefix + name}},
		}

		operations = append(operations,
			rawir.RawOperation{
				Group:       name,
				OperationID: name + "_List",
				Summary:     "List " + name,
				Method:      "GET",
				Path:        collection,
				Parameters: []rawir.RawParameter{
					{Name: "page_size", In: "query", Type: "integer"},
					{Name: "page_token", In: "query", Type: "string"},
					{Name: "order_by", In: "query", Type: "string", Enum: []string{"name", "created_at"}},
					{Name: "x-request-id", In: "header", Type: "string"},
				},
				Responses: map[string]*rawir.RawResponse{
					"200": {MediaType: "application/json", Schema: &rawir.RawSchema{Ref: rawir.RefPrefix + name + "List"}},
				},
			},
			rawir.RawOperation{
				Group:       name,
				OperationID: name + "_Create",
				Summary:     "Create " + name,
				Method:      "POST",
				Path:        collection,
				RequestBody: &rawir.RawRequestBody{
					Required:  true,
					MediaType: "application/json",
					Schema:    &rawir.RawSchema{Ref: rawir.RefPrefix + name},
				},
				Responses: itemResponse,
			},
			rawir.RawOperation{
				Group:       name,
				OperationID: name + "_Get",
				Summary:     "Get " + name,
				Method:      "GET",
				Path:        collection + "/{resource_id}",
				Parameters: []rawir.RawParameter{
					{Name: "resource_id", In: "path", Type: "string", Required: true},
				},
				Responses: itemResponse,
			},
			// No operationId: exercises the synthesized name and use fallback.
			rawir.RawOperation{
				Group:   name,
				Summary: "Delete " + name,
				Method:  "DELETE",
				Path:    collection + "/{resource_id}",
				Parameters: []rawir.RawParameter{
					{Name: "resource_id", In: "path", Type: "string", Required: true},
				},
				Responses: map[string]*rawir.RawResponse{"200": {MediaType: "application/json"}},
			},
		)
	}

	return &rawir.RawModule{Name: "bench", Operations: operations, Schemas: schemas}
}

func BenchmarkNormalize(b *testing.B) {
	cases := []struct {
		name      string
		resources int
	}{
		{"small", 4},
		{"large", 48},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			mod := benchModule(tc.resources)
			for b.Loop() {
				if specs := Normalize(mod); len(specs) == 0 {
					b.Fatal("Normalize produced no specs")
				}
			}
		})
	}
}
