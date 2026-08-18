package swagger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestConvertSchema_PreservesReadOnly(t *testing.T) {
	var schema schemaNode
	if err := json.Unmarshal([]byte(`{"type":"string","readOnly":true}`), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if got := convertSchema(&schema); got == nil || !got.ReadOnly {
		t.Fatalf("schema = %#v, want readOnly", got)
	}
}

func TestConvertSchema_IgnoresReferenceSiblings(t *testing.T) {
	got := convertSchema(&schemaNode{
		Ref:      rawir.RefPrefix + "Resource",
		Type:     "string",
		ReadOnly: true,
		Required: []string{"name"},
	})
	if got.Ref != rawir.RefPrefix+"Resource" || got.Type != "" || got.ReadOnly || len(got.Required) != 0 {
		t.Fatalf("schema = %#v, want reference without ignored siblings", got)
	}
}

func TestConvertSchema_PreservesSupportedCompositionMetadata(t *testing.T) {
	var schema schemaNode
	input := `{
  "allOf": [
    {"type":"object","required":["name"],"properties":{"name":{"type":"string"}}},
    {"type":"object","properties":{"labels":{"type":"object","additionalProperties":{"type":"integer"}}}}
  ]
}`
	if err := json.Unmarshal([]byte(input), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	got := convertSchema(&schema)
	if len(got.AllOf) != 2 || got.AllOf[0].Properties["name"] == nil || !reflect.DeepEqual(got.AllOf[0].Required, []string{"name"}) {
		t.Fatalf("allOf = %#v, want required name branch", got.AllOf)
	}
	labels := got.AllOf[1].Properties["labels"]
	if labels == nil || labels.AdditionalProperties == nil || labels.AdditionalProperties.Schema == nil || labels.AdditionalProperties.Schema.Type != "integer" {
		t.Fatalf("labels = %#v, want integer additionalProperties", labels)
	}
}

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
			testutil.AssertRawModuleGolden(t, tc.name, mod)
		})
	}
}

func TestParse_YAMLMatchesJSON(t *testing.T) {
	syncDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(syncDir, "petstore.json"), []byte(petstoreMinInput), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "petstore.yaml"), []byte(petstoreMinYAMLInput), 0o644); err != nil {
		t.Fatal(err)
	}

	parse := func(file string) *rawir.RawModule {
		t.Helper()
		src := &sourceconfig.Source{
			Name:    "demo",
			Swagger: &sourceconfig.SwaggerConfig{Files: []string{file}},
		}
		mod, err := Parse(src, syncDir)
		if err != nil {
			t.Fatalf("Parse(%s): %v", file, err)
		}
		return mod
	}

	jsonModule := parse("petstore.json")
	yamlModule := parse("petstore.yaml")
	for _, module := range []*rawir.RawModule{jsonModule, yamlModule} {
		sort.Slice(module.Operations, func(i, j int) bool {
			return module.Operations[i].Method+" "+module.Operations[i].Path <
				module.Operations[j].Method+" "+module.Operations[j].Path
		})
	}
	if !reflect.DeepEqual(jsonModule, yamlModule) {
		t.Fatalf("YAML module differs from JSON\nJSON: %#v\nYAML: %#v", jsonModule, yamlModule)
	}
}

func TestParse_DeduplicatesSwaggerParameters(t *testing.T) {
	syncDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(syncDir, "duplicate.yaml"), []byte(duplicateParameterYAMLInput), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &sourceconfig.Source{
		Name:    "demo",
		Swagger: &sourceconfig.SwaggerConfig{Files: []string{"duplicate.yaml"}},
	}

	mod, err := Parse(src, syncDir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := len(mod.Operations[0].Parameters); got != 1 {
		t.Fatalf("parameters = %d, want one unique parameter", got)
	}
}

func TestParse_PreservesRequestBodyMediaType(t *testing.T) {
	tests := []struct {
		name              string
		documentConsumes  string
		operationConsumes string
		want              string
	}{
		{name: "document consumes", documentConsumes: `,"consumes":["application/xml"]`, want: "application/xml"},
		{name: "operation override", documentConsumes: `,"consumes":["application/xml"]`, operationConsumes: `,"consumes":["application/json"]`, want: "application/json"},
		{name: "prefer json", operationConsumes: `,"consumes":["application/xml","application/json"]`, want: "application/json"},
		{name: "prefer exact json", operationConsumes: `,"consumes":["application/vnd.demo+json","application/json"]`, want: "application/json"},
		{name: "prefer vendor json", operationConsumes: `,"consumes":["application/xml","application/vnd.demo+json"]`, want: "application/vnd.demo+json"},
		{name: "unspecified"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"swagger":"2.0"` + tc.documentConsumes + `,"paths":{"/items":{"post":{"operationId":"Items_Create"` + tc.operationConsumes + `,"parameters":[{"name":"body","in":"body","schema":{"type":"object"}}],"responses":{"200":{}}}}}}`
			syncDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(syncDir, "swagger.json"), []byte(input), 0o644); err != nil {
				t.Fatal(err)
			}
			src := &sourceconfig.Source{Name: "demo", Swagger: &sourceconfig.SwaggerConfig{Files: []string{"swagger.json"}}}
			module, err := Parse(src, syncDir)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := module.Operations[0].RequestBody.MediaType; got != tc.want {
				t.Fatalf("raw media type = %q, want %q", got, tc.want)
			}
			if got := normalize.Normalize(module)[0].RequestBody.MediaType; got != tc.want {
				t.Fatalf("runtime media type = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParse_SecuritySemantics(t *testing.T) {
	cases := []struct {
		name              string
		documentSecurity  string
		operationSecurity string
		wantPublic        bool
		wantScopes        []string
	}{
		{name: "absent is public", wantPublic: true},
		{name: "document inherited", documentSecurity: `,"security":[{"oauth":["read"]}]`, wantScopes: []string{"read"}},
		{name: "operation empty is public", documentSecurity: `,"security":[{"oauth":["read"]}]`, operationSecurity: `,"security":[]`, wantPublic: true},
		{name: "operation overrides document", documentSecurity: `,"security":[{"oauth":["read"]}]`, operationSecurity: `,"security":[{"oauth":["write"]}]`, wantScopes: []string{"write"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"swagger":"2.0"` + tc.documentSecurity + `,"paths":{"/health":{"get":{"operationId":"Health_Get"` + tc.operationSecurity + `,"responses":{"200":{}}}}}}`
			syncDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(syncDir, "swagger.json"), []byte(input), 0o644); err != nil {
				t.Fatal(err)
			}
			src := &sourceconfig.Source{Name: "demo", Swagger: &sourceconfig.SwaggerConfig{Files: []string{"swagger.json"}}}
			mod, err := Parse(src, syncDir)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			security := normalize.Normalize(mod)[0].Security
			if security == nil || security.Public != tc.wantPublic || !reflect.DeepEqual(security.Scopes, tc.wantScopes) {
				t.Fatalf("security = %#v, want public=%t scopes=%v", security, tc.wantPublic, tc.wantScopes)
			}
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

const petstoreMinYAMLInput = `swagger: "2.0"
definitions:
  Pet:
    type: object
    properties:
      id:
        type: integer
      name:
        type: string
paths:
  /pets:
    get:
      operationId: Pet_List
      tags: [Pets]
      summary: List pets.
      responses:
        "200":
          schema:
            type: array
            items:
              $ref: "#/definitions/Pet"
  /pets/{id}:
    get:
      operationId: Pet_Get
      tags: [Pets]
      summary: Get one pet.
      parameters:
        - name: id
          in: path
          required: true
          type: string
      responses:
        "200":
          schema:
            $ref: "#/definitions/Pet"
`

const duplicateParameterYAMLInput = `swagger: "2.0"
paths:
  /jit:
    get:
      parameters:
        - {name: network, in: query, required: true, type: string}
        - {name: network, in: query, required: true, type: string}
      responses:
        "200": {description: ok}
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
