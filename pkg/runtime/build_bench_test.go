package runtime

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"
)

// benchSpecs builds the CommandSpec slice a generated module carries: one
// group per resource with list/create/get/delete commands, mixed parameter
// locations, request bodies, and list output hints.
func benchSpecs(resources int) []CommandSpec {
	specs := make([]CommandSpec, 0, resources*4)

	for i := range resources {
		group := fmt.Sprintf("resource%d", i)
		path := fmt.Sprintf("/v1/resource%ds", i)
		body := &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Schema: &SchemaSpec{
				Type: "object",
				Properties: map[string]*SchemaSpec{
					"name":        {Type: "string"},
					"description": {Type: "string"},
					"enabled":     {Type: "boolean"},
					"size":        {Type: "integer"},
					"tags":        {Type: "array", Items: &SchemaSpec{Type: "string"}},
					"owner": {Type: "object", Properties: map[string]*SchemaSpec{
						"id":    {Type: "string"},
						"email": {Type: "string"},
					}},
				},
				Required: []string{"name"},
			},
		}

		specs = append(specs,
			CommandSpec{
				Group:       group,
				Use:         "list",
				Short:       "List " + group,
				Long:        "List every " + group + " visible to the caller.",
				OperationID: group + "_List",
				Method:      "GET",
				PathTpl:     path,
				Params: []ParamSpec{
					{Name: "page_size", Flag: "page-size", Aliases: []string{"page_size"}, In: InQuery, GoType: "int64", Help: "Page size"},
					{Name: "page_token", Flag: "page-token", Aliases: []string{"page_token"}, In: InQuery, GoType: "string", Help: "Page token"},
					{Name: "order_by", Flag: "order-by", Aliases: []string{"order_by"}, In: InQuery, GoType: "string", Help: "Sort order", Enum: []string{"name", "created_at"}},
					{Name: "x-request-id", Flag: "x-request-id", In: InHeader, GoType: "string", Help: "Request id"},
				},
				Output: OutputHints{
					ListPath:          "items",
					DefaultColumns:    []string{"id", "name", "enabled"},
					ResponseMediaType: "application/json",
					Pagination: &PaginationHint{
						Strategy:   "token",
						TokenParam: "page_token",
						TokenField: "next_page_token",
						LimitParam: "page_size",
					},
				},
			},
			CommandSpec{
				Group:       group,
				Use:         "create",
				Short:       "Create " + group,
				OperationID: group + "_Create",
				Method:      "POST",
				PathTpl:     path,
				RequestBody: body,
				Output:      OutputHints{ResponseMediaType: "application/json"},
			},
			CommandSpec{
				Group:       group,
				Use:         "get",
				Short:       "Get " + group,
				OperationID: group + "_Get",
				Method:      "GET",
				PathTpl:     path + "/{resource_id}",
				Params: []ParamSpec{
					{Name: "resource_id", Flag: "resource-id", Aliases: []string{"resource_id"}, Argument: "resource-id", In: InPath, GoType: "string", Help: "Resource id", Required: true},
				},
				Output: OutputHints{ResponseMediaType: "application/json"},
			},
			CommandSpec{
				Group:       group,
				Use:         "delete",
				Short:       "Delete " + group,
				OperationID: group + "_Delete",
				Method:      "DELETE",
				PathTpl:     path + "/{resource_id}",
				Params: []ParamSpec{
					{Name: "resource_id", Flag: "resource-id", Aliases: []string{"resource_id"}, Argument: "resource-id", In: InPath, GoType: "string", Help: "Resource id", Required: true},
				},
			},
		)
	}
	return specs
}

// BenchmarkBuild measures the cobra command tree construction every generated
// CLI pays on process start, before any command runs.
func BenchmarkBuild(b *testing.B) {
	cases := []struct {
		name      string
		resources int
	}{
		{"small", 4},
		{"large", 48},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			specs := benchSpecs(tc.resources)
			for b.Loop() {
				root := &cobra.Command{Use: "benchctl"}
				root.AddGroup(&cobra.Group{ID: ModuleGroupID, Title: "Modules"})
				if err := Build(root, "demo", specs); err != nil {
					b.Fatalf("Build: %v", err)
				}
			}
		})
	}
}

func BenchmarkBuildFlat(b *testing.B) {
	specs := benchSpecs(48)
	for b.Loop() {
		root := &cobra.Command{Use: "benchctl"}
		root.AddGroup(&cobra.Group{ID: ModuleGroupID, Title: "Modules"})
		if err := BuildFlat(root, "demo", specs); err != nil {
			b.Fatalf("BuildFlat: %v", err)
		}
	}
}
