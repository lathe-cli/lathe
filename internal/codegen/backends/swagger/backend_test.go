package swagger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestParse_Golden(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"petstore-min", petstoreMinInput},
		{"ref-resolution", refResolutionInput},
		{"path-and-query-params", pathAndQueryParamsInput},
		{"header-and-formdata", headerAndFormDataInput},
		{"tags-fallback", tagsFallbackInput},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			syncDir := t.TempDir()
			inputPath := filepath.Join(syncDir, tc.name+".swagger.json")
			if err := os.WriteFile(inputPath, []byte(tc.input), 0o644); err != nil {
				t.Fatalf("seed swagger input: %v", err)
			}

			src := &sourceconfig.Source{
				Name: "demo",
				Swagger: &sourceconfig.SwaggerConfig{
					Files: []string{tc.name + ".swagger.json"},
				},
			}
			mod, err := Parse(src, syncDir)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			sort.Slice(mod.Operations, func(i, j int) bool {
				if mod.Operations[i].Path != mod.Operations[j].Path {
					return mod.Operations[i].Path < mod.Operations[j].Path
				}
				return mod.Operations[i].Method < mod.Operations[j].Method
			})

			data, err := json.MarshalIndent(mod, "", "  ")
			if err != nil {
				t.Fatalf("marshal rawir: %v", err)
			}
			data = append(data, '\n')
			testutil.AssertGolden(t, filepath.Join("testdata", tc.name+".golden.json"), data)
		})
	}
}

const petstoreMinInput = `{
  "swagger": "2.0",
  "definitions": {
    "Pet": {
      "type": "object",
      "properties": {
        "id": {"type": "integer"},
        "name": {"type": "string"}
      }
    }
  },
  "paths": {
    "/pets": {
      "get": {
        "operationId": "Pet_List",
        "tags": ["Pets"],
        "summary": "List pets.",
        "responses": {"200": {"schema": {"type": "array", "items": {"$ref": "#/definitions/Pet"}}}}
      }
    },
    "/pets/{id}": {
      "get": {
        "operationId": "Pet_Get",
        "tags": ["Pets"],
        "summary": "Get one pet.",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "type": "string"}
        ],
        "responses": {"200": {"schema": {"$ref": "#/definitions/Pet"}}}
      }
    }
  }
}
`

const refResolutionInput = `{
  "swagger": "2.0",
  "definitions": {
    "Pet": {
      "type": "object",
      "properties": {
        "name": {"type": "string"}
      }
    }
  },
  "paths": {
    "/pets": {
      "post": {
        "operationId": "Pet_Create",
        "tags": ["Pets"],
        "summary": "Create a pet.",
        "parameters": [
          {"name": "body", "in": "body", "required": true, "schema": {"$ref": "#/definitions/Pet"}}
        ],
        "responses": {"200": {"schema": {"$ref": "#/definitions/Pet"}}}
      }
    }
  }
}
`

const pathAndQueryParamsInput = `{
  "swagger": "2.0",
  "paths": {
    "/users/{id}": {
      "get": {
        "operationId": "User_Get",
        "tags": ["Users"],
        "summary": "Get a user.",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "type": "string"},
          {"name": "limit", "in": "query", "required": false, "type": "integer", "description": "Max rows."}
        ],
        "responses": {}
      }
    }
  }
}
`

const headerAndFormDataInput = `{
  "swagger": "2.0",
  "paths": {
    "/uploads": {
      "post": {
        "operationId": "Uploads_Create",
        "tags": ["Uploads"],
        "summary": "Upload a file.",
        "parameters": [
          {"name": "X-Request-Id", "in": "header", "required": false, "type": "string", "description": "Trace id."},
          {"name": "file", "in": "formData", "required": true, "type": "string", "description": "Binary content."}
        ],
        "responses": {}
      }
    }
  }
}
`

const tagsFallbackInput = `{
  "swagger": "2.0",
  "paths": {
    "/health": {
      "get": {
        "operationId": "Health_Check",
        "summary": "Health check.",
        "responses": {}
      }
    }
  }
}
`
