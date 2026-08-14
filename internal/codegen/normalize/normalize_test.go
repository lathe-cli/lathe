package normalize

import (
	"reflect"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

// Each case supplies a constructed rawir.RawModule and asserts the
// Normalize output against testdata/<name>.golden.json.
func TestNormalize_Golden(t *testing.T) {
	cases := []struct {
		name  string
		input func() *rawir.RawModule
	}{
		{"minimal-get", minimalGet},
		{"request-body-required", requestBodyRequired},
		{"request-body-optional", requestBodyOptional},
		{"request-body-ref-schema", requestBodyRefSchema},
		{"list-response", listResponse},
		{"no-op-id", noOpID},
		{"multiple-methods-same-path", multipleMethodsSamePath},
		{"shared-endpoint-envelope", sharedEndpointEnvelope},
		{"request-body-envelope", requestBodyEnvelope},
		{"param-in-header", paramInHeader},
		{"param-in-form-data", paramInFormData},
		{"pagination-cursor", paginationCursor},
		{"streaming-sse", streamingSSE},
		{"response-media-type", responseMediaType},
		{"security-public", securityPublic},
		{"security-scopes", securityScopes},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			specs := Normalize(tc.input())
			testutil.AssertNamedJSONGolden(t, tc.name, specs)
		})
	}
}

func TestNormalize_SuccessResponseHints(t *testing.T) {
	listSchema := func(listPath string) *rawir.RawSchema {
		return &rawir.RawSchema{
			Type: "object",
			Properties: map[string]*rawir.RawSchema{
				listPath:          {Type: "array", Items: &rawir.RawSchema{Ref: rawir.RefPrefix + "Item"}},
				"next_page_token": {Type: "string"},
			},
		}
	}
	response := func(mediaType, listPath string) *rawir.RawResponse {
		return &rawir.RawResponse{MediaType: mediaType, Schema: listSchema(listPath)}
	}
	normalize := func(responses map[string]*rawir.RawResponse, produces ...string) runtime.OutputHints {
		t.Helper()
		mod := &rawir.RawModule{
			Name: "demo",
			Schemas: map[string]*rawir.RawSchema{
				"Item": {Type: "object", Properties: map[string]*rawir.RawSchema{
					"id":   {Type: "string"},
					"name": {Type: "string"},
				}},
			},
			Operations: []rawir.RawOperation{{
				Group:       "Items",
				OperationID: "Items_List",
				Method:      "GET",
				Path:        "/items",
				Parameters: []rawir.RawParameter{
					{Name: "page_token", In: "query", Type: "string"},
					{Name: "limit", In: "query", Type: "integer"},
				},
				Responses: responses,
				Produces:  produces,
			}},
		}
		return Normalize(mod)[0].Output
	}

	jsonHints := runtime.OutputHints{
		ListPath:          "items",
		DefaultColumns:    []string{"name", "id"},
		ResponseMediaType: "application/json",
		Pagination: &runtime.PaginationHint{
			Strategy:   "cursor",
			TokenParam: "page_token",
			TokenField: "next_page_token",
			LimitParam: "limit",
		},
	}

	tests := []struct {
		name      string
		responses map[string]*rawir.RawResponse
		produces  []string
		want      runtime.OutputHints
	}{
		{
			name: "200 remains authoritative",
			responses: map[string]*rawir.RawResponse{
				"200": response("application/json", "items"),
				"201": response("text/event-stream", "data"),
			},
			want: jsonHints,
		},
		{
			name: "200 keeps produces fallback",
			responses: map[string]*rawir.RawResponse{
				"200": response("", "items"),
				"201": response("application/xml", "data"),
			},
			produces: []string{"application/json"},
			want:     jsonHints,
		},
		{
			name:      "lone 201 matches 200",
			responses: map[string]*rawir.RawResponse{"201": response("application/json", "items")},
			want:      jsonHints,
		},
		{
			name: "identical JSON schemas retain hints",
			responses: map[string]*rawir.RawResponse{
				"201": response("application/json", "items"),
				"202": response("application/json", "items"),
			},
			want: jsonHints,
		},
		{
			name: "compatible JSON media retain schema hints only",
			responses: map[string]*rawir.RawResponse{
				"201": response("application/json", "items"),
				"202": response("application/vnd.demo+json", "items"),
			},
			want: runtime.OutputHints{
				ListPath:       jsonHints.ListPath,
				DefaultColumns: jsonHints.DefaultColumns,
				Pagination:     jsonHints.Pagination,
			},
		},
		{
			name: "incompatible schemas omit schema hints",
			responses: map[string]*rawir.RawResponse{
				"201": response("application/json", "items"),
				"202": response("application/json", "data"),
			},
			want: runtime.OutputHints{ResponseMediaType: "application/json"},
		},
		{
			name: "mixed JSON and streaming media omit unsafe hints",
			responses: map[string]*rawir.RawResponse{
				"201": response("application/json", "items"),
				"202": response("text/event-stream", "items"),
			},
		},
		{
			name: "mixed streaming strategies omit streaming hint",
			responses: map[string]*rawir.RawResponse{
				"201": {MediaType: "text/event-stream"},
				"202": {MediaType: "application/x-ndjson"},
			},
		},
		{
			name: "compatible streaming strategies retain streaming hint",
			responses: map[string]*rawir.RawResponse{
				"201": {MediaType: "application/x-ndjson"},
				"202": {MediaType: "application/stream+json"},
			},
			want: runtime.OutputHints{Streaming: &runtime.StreamingHint{Strategy: "ndjson"}},
		},
		{
			name: "homogeneous streaming responses retain streaming hint",
			responses: map[string]*rawir.RawResponse{
				"201": {MediaType: "text/event-stream"},
				"202": {MediaType: "text/event-stream"},
			},
			want: runtime.OutputHints{
				ResponseMediaType: "text/event-stream",
				Streaming:         &runtime.StreamingHint{Strategy: "sse"},
			},
		},
		{
			name:      "non JSON schema does not expose JSON hints",
			responses: map[string]*rawir.RawResponse{"201": response("application/xml", "items")},
			want:      runtime.OutputHints{ResponseMediaType: "application/xml"},
		},
		{
			name: "missing schema invalidates schema hints",
			responses: map[string]*rawir.RawResponse{
				"201": response("application/json", "items"),
				"204": {MediaType: "application/json"},
			},
			want: runtime.OutputHints{ResponseMediaType: "application/json"},
		},
		{
			name:      "unknown media preserves schema hints",
			responses: map[string]*rawir.RawResponse{"201": response("", "items")},
			want: runtime.OutputHints{
				ListPath:       jsonHints.ListPath,
				DefaultColumns: jsonHints.DefaultColumns,
				Pagination:     jsonHints.Pagination,
			},
		},
		{
			name:      "single produces media applies to explicit 201",
			responses: map[string]*rawir.RawResponse{"201": response("", "items")},
			produces:  []string{"application/json"},
			want:      jsonHints,
		},
		{
			name:      "mixed produces media invalidates JSON hints",
			responses: map[string]*rawir.RawResponse{"201": response("", "items")},
			produces:  []string{"application/json", "application/xml"},
		},
		{
			name: "non explicit and non success keys are ignored",
			responses: map[string]*rawir.RawResponse{
				"201":     response("application/json", "items"),
				"default": response("application/xml", "data"),
				"2XX":     response("text/event-stream", "data"),
				"300":     response("application/xml", "data"),
				"success": response("application/xml", "data"),
			},
			want: jsonHints,
		},
		{
			name: "produces alone is not a success response",
			responses: map[string]*rawir.RawResponse{
				"default": response("application/json", "items"),
				"2XX":     response("application/json", "items"),
			},
			produces: []string{"application/json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalize(tt.responses, tt.produces...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Output = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func minimalGet() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Users",
			OperationID: "Users_GetUser",
			Summary:     "Get a user by ID.",
			Method:      "GET",
			Path:        "/users/{id}",
			Parameters: []rawir.RawParameter{
				{Name: "id", In: "path", Required: true, Type: "string"},
				{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Max rows."},
			},
			Responses: map[string]*rawir.RawResponse{},
		}},
	}
}

func requestBodyRequired() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Users",
			OperationID: "Users_CreateUser",
			Summary:     "Create a user.",
			Method:      "POST",
			Path:        "/users",
			RequestBody: &rawir.RawRequestBody{Required: true},
			Responses:   map[string]*rawir.RawResponse{},
		}},
	}
}

func requestBodyOptional() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Users",
			OperationID: "Users_PatchUser",
			Summary:     "Patch a user.",
			Method:      "PATCH",
			Path:        "/users/{id}",
			Parameters: []rawir.RawParameter{
				{Name: "id", In: "path", Required: true, Type: "string"},
			},
			RequestBody: &rawir.RawRequestBody{Required: false},
			Responses:   map[string]*rawir.RawResponse{},
		}},
	}
}

func requestBodyRefSchema() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Schemas: map[string]*rawir.RawSchema{
			"Pet": {Type: "object", Properties: map[string]*rawir.RawSchema{"name": {Type: "string"}}},
		},
		Operations: []rawir.RawOperation{{
			Group:       "Pets",
			OperationID: "Pets_CreatePet",
			Summary:     "Create a pet.",
			Method:      "POST",
			Path:        "/pets",
			RequestBody: &rawir.RawRequestBody{Required: true, Schema: &rawir.RawSchema{Ref: rawir.RefPrefix + "Pet"}},
			Responses:   map[string]*rawir.RawResponse{},
		}},
	}
}

func listResponse() *rawir.RawModule {
	item := &rawir.RawSchema{
		Type: "object",
		Properties: map[string]*rawir.RawSchema{
			"id":   {Type: "string"},
			"name": {Type: "string"},
			"age":  {Type: "integer"},
		},
	}
	envelope := &rawir.RawSchema{
		Type: "object",
		Properties: map[string]*rawir.RawSchema{
			"items": {Type: "array", Items: &rawir.RawSchema{Ref: rawir.RefPrefix + "Item"}},
		},
	}
	return &rawir.RawModule{
		Name: "demo",
		Schemas: map[string]*rawir.RawSchema{
			"Item":     item,
			"ItemList": envelope,
		},
		Operations: []rawir.RawOperation{{
			Group:       "Items",
			OperationID: "Items_List",
			Summary:     "List items.",
			Method:      "GET",
			Path:        "/items",
			Responses: map[string]*rawir.RawResponse{
				"200": {Schema: envelope},
			},
		}},
	}
}

func noOpID() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Users",
			OperationID: "",
			Method:      "GET",
			Path:        "/users",
			Responses:   map[string]*rawir.RawResponse{},
		}},
	}
}

func multipleMethodsSamePath() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{
			{
				Group:       "Resources",
				OperationID: "Resources_List",
				Summary:     "List resources.",
				Method:      "GET",
				Path:        "/resources",
				Responses:   map[string]*rawir.RawResponse{},
			},
			{
				Group:       "Resources",
				OperationID: "Resources_Create",
				Summary:     "Create a resource.",
				Method:      "POST",
				Path:        "/resources",
				RequestBody: &rawir.RawRequestBody{Required: true},
				Responses:   map[string]*rawir.RawResponse{},
			},
		},
	}
}

func requestBodyEnvelope() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Apps",
			OperationID: "Apps_CreateApp",
			Summary:     "Create an app.",
			Method:      "POST",
			Path:        "/graphql",
			RequestBody: &rawir.RawRequestBody{
				Required:  true,
				MediaType: "application/json",
				Template:  `{"query":"mutation CreateApp($name:String!){createApp(name:$name){id}}","variables":{}}`,
				MergePath: "variables",
			},
			Responses: map[string]*rawir.RawResponse{},
		}},
	}
}

func sharedEndpointEnvelope() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{
			{
				Group:       "Apps",
				OperationID: "Apps_CreateApp",
				Summary:     "Create an app.",
				Method:      "POST",
				Path:        "/graphql",
				RequestBody: &rawir.RawRequestBody{
					Required:  true,
					MediaType: "application/json",
					Template:  `{"query":"mutation CreateApp{createApp{id}}","variables":{}}`,
					MergePath: "variables",
				},
				Responses: map[string]*rawir.RawResponse{},
			},
			{
				Group:       "Apps",
				OperationID: "Apps_ListApps",
				Summary:     "List apps.",
				Method:      "POST",
				Path:        "/graphql",
				RequestBody: &rawir.RawRequestBody{
					Required:  true,
					MediaType: "application/json",
					Template:  `{"query":"query ListApps{apps{id}}","variables":{}}`,
					MergePath: "variables",
				},
				Responses: map[string]*rawir.RawResponse{},
			},
		},
	}
}

func paramInHeader() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Users",
			OperationID: "Users_GetUser",
			Summary:     "Get a user.",
			Method:      "GET",
			Path:        "/users/{id}",
			Parameters: []rawir.RawParameter{
				{Name: "id", In: "path", Required: true, Type: "string"},
				{Name: "X-Request-Id", In: "header", Required: false, Type: "string", Description: "Trace id."},
			},
			Responses: map[string]*rawir.RawResponse{},
		}},
	}
}

func paramInFormData() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Uploads",
			OperationID: "Uploads_Create",
			Summary:     "Upload a file.",
			Method:      "POST",
			Path:        "/uploads",
			Parameters: []rawir.RawParameter{
				{Name: "file", In: "formData", Required: true, Type: "string", Description: "Binary content."},
			},
			Responses: map[string]*rawir.RawResponse{},
		}},
	}
}

func paginationCursor() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Schemas: map[string]*rawir.RawSchema{
			"Item": {Type: "object", Properties: map[string]*rawir.RawSchema{
				"id":   {Type: "string"},
				"name": {Type: "string"},
			}},
		},
		Operations: []rawir.RawOperation{{
			Group:       "Items",
			OperationID: "Items_List",
			Summary:     "List items with pagination.",
			Method:      "GET",
			Path:        "/items",
			Parameters: []rawir.RawParameter{
				{Name: "page_token", In: "query", Type: "string", Description: "Pagination token."},
				{Name: "limit", In: "query", Type: "integer", Description: "Page size."},
			},
			Responses: map[string]*rawir.RawResponse{
				"200": {Schema: &rawir.RawSchema{
					Type: "object",
					Properties: map[string]*rawir.RawSchema{
						"items":           {Type: "array", Items: &rawir.RawSchema{Ref: rawir.RefPrefix + "Item"}},
						"next_page_token": {Type: "string"},
					},
				}},
			},
		}},
	}
}

func streamingSSE() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Events",
			OperationID: "Events_Watch",
			Summary:     "Watch events.",
			Method:      "GET",
			Path:        "/events",
			Produces:    []string{"text/event-stream"},
			Responses: map[string]*rawir.RawResponse{
				"200": {MediaType: "text/event-stream"},
			},
		}},
	}
}

func responseMediaType() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Reports",
			OperationID: "Reports_Download",
			Summary:     "Download report.",
			Method:      "GET",
			Path:        "/reports/{id}/download",
			Parameters: []rawir.RawParameter{
				{Name: "id", In: "path", Required: true, Type: "string"},
			},
			Produces: []string{"application/pdf"},
			Responses: map[string]*rawir.RawResponse{
				"200": {MediaType: "application/pdf"},
			},
		}},
	}
}

func securityPublic() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Health",
			OperationID: "Health_Check",
			Summary:     "Health check.",
			Method:      "GET",
			Path:        "/healthz",
			Responses:   map[string]*rawir.RawResponse{},
			Security:    []rawir.RawSecurityReq{},
		}},
	}
}

func securityScopes() *rawir.RawModule {
	return &rawir.RawModule{
		Name: "demo",
		Operations: []rawir.RawOperation{{
			Group:       "Pets",
			OperationID: "Pets_Delete",
			Summary:     "Delete a pet.",
			Method:      "DELETE",
			Path:        "/pets/{id}",
			Parameters: []rawir.RawParameter{
				{Name: "id", In: "path", Required: true, Type: "string"},
			},
			Responses: map[string]*rawir.RawResponse{},
			Security: []rawir.RawSecurityReq{
				{Scopes: []string{"write:pets", "read:pets"}},
			},
		}},
	}
}

func TestDisambiguateUseCollisions(t *testing.T) {
	// Same group, same derived name: distinct operations must both survive with
	// distinct command names rather than aborting codegen.
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/groups"},
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/Groups"},
	}}
	specs := Normalize(mod)
	if len(specs) != 2 {
		t.Fatalf("want 2 specs, got %d", len(specs))
	}
	if specs[0].Use == specs[1].Use {
		t.Errorf("colliding command names not disambiguated: both %q", specs[0].Use)
	}
}

// A leading id segment is redundant only when it repeats the group. Stripping
// it unconditionally collapsed every CRUD verb onto the same command name.
func TestOpNameKeepsVerbWhenPrefixIsNotTheGroup(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Chunk", OperationID: "create_chunk", Method: "POST", Path: "/api/chunk"},
		{Group: "Chunk", OperationID: "update_chunk", Method: "PUT", Path: "/api/chunk"},
		{Group: "Chunk", OperationID: "delete_chunk", Method: "DELETE", Path: "/api/chunk"},
		{Group: "Chunks", OperationID: "Chunks_search", Method: "POST", Path: "/api/chunk/search"},
	}}
	got := map[string]string{}
	for _, spec := range Normalize(mod) {
		got[spec.OperationID] = spec.Use
	}
	want := map[string]string{
		"create_chunk":  "create-chunk",
		"update_chunk":  "update-chunk",
		"delete_chunk":  "delete-chunk",
		"Chunks_search": "search",
	}
	for id, expected := range want {
		if got[id] != expected {
			t.Errorf("%s: want Use %q, got %q", id, expected, got[id])
		}
	}
}

func TestOpNameDropsModulePrefix(t *testing.T) {
	mod := &rawir.RawModule{
		Name: "console",
		Operations: []rawir.RawOperation{{
			Group: "Apps", OperationID: "console_listApps", Method: "POST", Path: "/graphql",
		}},
	}
	specs := Normalize(mod)
	if got := specs[0].Use; got != "list-apps" {
		t.Fatalf("Use = %q, want list-apps", got)
	}
}

func TestSynthUseNameDropsSharedNoisePrefix(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Dashboards", Method: "GET", Path: "/api/v1/dashboard/"},
		{Group: "Dashboards", Method: "DELETE", Path: "/api/v1/dashboard/{pk}/favorites/"},
		{Group: "Charts", Method: "POST", Path: "/api/v1/advanced_data_type/convert"},
	}}
	want := map[string]string{
		"GET":    "get-dashboard",
		"DELETE": "delete-dashboard-pk-favorites",
		"POST":   "post-advanced-data-type-convert",
	}
	for _, spec := range Normalize(mod) {
		if want[spec.Method] != spec.Use {
			t.Errorf("%s %s: want Use %q, got %q", spec.Method, spec.PathTpl, want[spec.Method], spec.Use)
		}
	}
}

// Two API versions in one spec must stay distinguishable: the version segment
// is only noise when every unnamed operation shares it.
func TestSynthUseNameKeepsDivergingVersions(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Users", Method: "GET", Path: "/api/v1/users"},
		{Group: "Users", Method: "GET", Path: "/api/v2/users"},
	}}
	specs := Normalize(mod)
	uses := []string{specs[0].Use, specs[1].Use}
	for _, use := range uses {
		if use != "get-v1-users" && use != "get-v2-users" {
			t.Errorf("want versioned command names, got %v", uses)
		}
	}
	if uses[0] == uses[1] {
		t.Errorf("versions collapsed onto one name: %v", uses)
	}
}

// An operationId can start or end with an underscore; the command name must not
// start with a dash, which cobra would read as a flag.
func TestKebabFromIDTrimsEdgeSeparators(t *testing.T) {
	cases := map[string]string{"_foo": "foo", "foo_": "foo", "a__b": "a-b", "_": ""}
	for id, want := range cases {
		if got := kebabFromID(id); got != want {
			t.Errorf("kebabFromID(%q) = %q, want %q", id, got, want)
		}
	}
}
