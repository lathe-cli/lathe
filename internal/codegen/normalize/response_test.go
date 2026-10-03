package normalize

import (
	"reflect"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestNormalize_BinaryResponses(t *testing.T) {
	fileSchema := &rawir.RawSchema{Type: "string", Format: "binary"}
	cases := []struct {
		name      string
		response  *rawir.RawResponse
		responses map[string]*rawir.RawResponse
		produces  []string
		schemas   map[string]*rawir.RawSchema
		want      bool
	}{
		{name: "octet-stream", response: &rawir.RawResponse{MediaType: "application/octet-stream"}, want: true},
		{name: "image/png", response: &rawir.RawResponse{MediaType: "image/png"}, want: true},
		{name: "image/svg+xml", response: &rawir.RawResponse{MediaType: "image/svg+xml"}, want: false},
		{
			name:     "vnd.ms-excel format binary",
			response: &rawir.RawResponse{MediaType: "application/vnd.ms-excel", Schema: &rawir.RawSchema{Ref: rawir.RefPrefix + "File"}},
			schemas:  map[string]*rawir.RawSchema{"File": fileSchema},
			want:     true,
		},
		{name: "vnd.ms-excel no schema", response: &rawir.RawResponse{MediaType: "application/vnd.ms-excel"}, want: false},
		{name: "json format binary", response: &rawir.RawResponse{MediaType: "application/json", Schema: fileSchema}, want: false},
		{name: "text/event-stream", response: &rawir.RawResponse{MediaType: "text/event-stream"}, want: false},
		{
			name:     "swagger octet-stream type file",
			response: &rawir.RawResponse{Schema: &rawir.RawSchema{Type: "file"}},
			produces: []string{"application/octet-stream"},
			want:     true,
		},
		{
			name:     "swagger produces pdf and json type file",
			response: &rawir.RawResponse{Schema: &rawir.RawSchema{Type: "file"}},
			produces: []string{"application/pdf", "application/json"},
			want:     true,
		},
		{name: "text/csv", response: &rawir.RawResponse{MediaType: "text/csv"}, want: false},
		{
			name: "200 pdf and 202 json",
			responses: map[string]*rawir.RawResponse{
				"200": {MediaType: "application/pdf", Schema: fileSchema},
				"202": {MediaType: "application/json", Schema: &rawir.RawSchema{Type: "object"}},
			},
			want: false,
		},
		{
			name: "200 pdf and 202 ndjson",
			responses: map[string]*rawir.RawResponse{
				"200": {MediaType: "application/pdf"},
				"202": {MediaType: "application/x-ndjson"},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := tc.responses
			if responses == nil {
				responses = map[string]*rawir.RawResponse{"200": tc.response}
			}
			got := Normalize(&rawir.RawModule{
				Name:    "demo",
				Schemas: tc.schemas,
				Operations: []rawir.RawOperation{{
					Group:       "Reports",
					OperationID: "Reports_Download",
					Method:      "GET",
					Path:        "/reports",
					Produces:    tc.produces,
					Responses:   responses,
				}},
			})
			testutil.Require(t, len(got) == 1, "specs = %d", len(got))
			testutil.Require(t, got[0].Output.Binary == tc.want, "Binary = %v, want %v", got[0].Output.Binary, tc.want)
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
			testutil.Require(t, reflect.DeepEqual(got, tt.want), "Output = %#v, want %#v", got, tt.want)
		})
	}
}
