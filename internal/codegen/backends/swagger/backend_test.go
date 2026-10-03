package swagger

import (
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
		t.Run(tc.name, func(t *testing.T) {
			mod := parseInput(t, tc.input, ".json")
			testutil.AssertRawModuleGolden(t, tc.name, mod)
		})
	}
}

func TestParse_YAMLMatchesJSON(t *testing.T) {
	jsonModule := parseInput(t, petstoreMinInput, ".json")
	yamlModule := parseInput(t, petstoreMinYAMLInput, ".yaml")
	for _, module := range []*rawir.RawModule{jsonModule, yamlModule} {
		sort.Slice(module.Operations, func(i, j int) bool {
			return module.Operations[i].Method+" "+module.Operations[i].Path <
				module.Operations[j].Method+" "+module.Operations[j].Path
		})
	}
	testutil.Require(t, reflect.DeepEqual(jsonModule, yamlModule), "YAML module differs from JSON\nJSON: %#v\nYAML: %#v", jsonModule, yamlModule)
}

func TestParse_DeduplicatesSwaggerParameters(t *testing.T) {
	mod := parseInput(t, duplicateParameterYAMLInput, ".yaml")
	if got := len(mod.Operations[0].Parameters); got != 1 {
		t.Fatalf("parameters = %d, want one unique parameter", got)
	}
}

func TestParse_SecuritySemantics(t *testing.T) {
	cases := []struct {
		name              string
		documentSecurity  string
		operationSecurity string
		wantUnspecified   bool
		wantPublic        bool
		wantScopes        []string
	}{
		// A document that never mentions security has not declared its
		// operations public; it has said nothing. Only an empty requirement
		// list is an explicit "no auth needed".
		{name: "absent is unspecified", wantUnspecified: true},
		{name: "document inherited", documentSecurity: `,"security":[{"oauth":["read"]}]`, wantScopes: []string{"read"}},
		{name: "document empty is public", documentSecurity: `,"security":[]`, wantPublic: true},
		{name: "operation empty is public", documentSecurity: `,"security":[{"oauth":["read"]}]`, operationSecurity: `,"security":[]`, wantPublic: true},
		{name: "operation overrides document", documentSecurity: `,"security":[{"oauth":["read"]}]`, operationSecurity: `,"security":[{"oauth":["write"]}]`, wantScopes: []string{"write"}},
		{name: "operation declared without document", operationSecurity: `,"security":[{"oauth":["write"]}]`, wantScopes: []string{"write"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"swagger":"2.0"` + tc.documentSecurity + `,"paths":{"/health":{"get":{"operationId":"Health_Get"` + tc.operationSecurity + `,"responses":{"200":{}}}}}}`
			mod := parseInput(t, input, ".json")
			security := normalize.Normalize(mod)[0].Security
			if tc.wantUnspecified {
				testutil.Require(t, security == nil, "security = %#v, want nil so the runtime keeps requiring auth", security)
				return
			}
			testutil.Require(t, security != nil && security.Public == tc.wantPublic && reflect.DeepEqual(security.Scopes, tc.wantScopes), "security = %#v, want public=%t scopes=%v", security, tc.wantPublic, tc.wantScopes)
		})
	}
}

func TestParse_PreservesBodySchemaMetadata(t *testing.T) {
	input := `{
	  "swagger": "2.0",
	  "paths": {
	    "/secrets": {
	      "post": {
	        "operationId": "Secrets_Create",
	        "parameters": [{
	          "name": "body",
	          "in": "body",
	          "required": true,
	          "schema": {
	            "type": "object",
	            "properties": {
	              "value": {"type": "string", "description": "Secret value", "format": "password", "enum": ["primary"]},
	              "labels": {"type": "object", "additionalProperties": {"type": "string"}}
	            }
	          }
	        }],
	        "responses": {"200": {}}
	      }
	    }
	  }
	}`
	mod := parseInput(t, input, ".json")
	schema := normalize.Normalize(mod)[0].RequestBody.Schema
	value := schema.Properties["value"]
	labels := schema.Properties["labels"]
	testutil.Require(t, value.Description == "Secret value" && value.Format == "password" && len(value.Enum) == 1 && value.Enum[0] == "primary", "value schema = %#v", value)
	testutil.Require(t, labels.AdditionalProperties != nil && labels.AdditionalProperties.Schema != nil && labels.AdditionalProperties.Schema.Type == "string", "labels schema = %#v", labels)
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

func parseInput(t *testing.T, input, ext string) *rawir.RawModule {
	t.Helper()
	dir := t.TempDir()
	file := "swagger" + ext
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(input), 0o644))
	mod, err := Parse(&sourceconfig.Source{Name: "demo", Swagger: &sourceconfig.SwaggerConfig{Files: []string{file}}}, dir)
	testutil.Require(t, err == nil, "%v", err)
	return mod
}

func TestParse_TagDescriptions(t *testing.T) {
	for _, tc := range []struct{ input, ext string }{
		{`{"swagger":"2.0","tags":[{"name":"Users","description":"Manage user accounts"}],"paths":{"/users":{"get":{"operationId":"listUsers","tags":["Users"],"responses":{}}},"/other":{"get":{"operationId":"listOther","tags":["Other"],"responses":{}}}}}`, ".json"},
		{"swagger: '2.0'\ntags:\n  - name: Users\n    description: Manage user accounts\npaths:\n  /users:\n    get:\n      operationId: listUsers\n      tags: [Users]\n      responses: {}\n  /other:\n    get:\n      operationId: listOther\n      tags: [Other]\n      responses: {}\n", ".yaml"},
	} {
		mod := parseInput(t, tc.input, tc.ext)
		for _, spec := range normalize.Normalize(mod) {
			want := ""
			if spec.Group == "Users" {
				want = "Manage user accounts"
			}
			testutil.Require(t, spec.GroupShort == want, "%s: group %q description = %q, want %q", tc.ext, spec.Group, spec.GroupShort, want)
		}
	}
}

func TestParse_FileParamContentType(t *testing.T) {
	spec := normalize.Normalize(parseInput(t, `{
  "swagger": "2.0",
  "paths": {
    "/uploads": {
      "post": {
        "operationId": "Uploads_Create",
        "parameters": [
          {"name": "file", "in": "formData", "type": "file", "required": true},
          {"name": "note", "in": "formData", "type": "string"}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`, ".json"))[0]
	for _, param := range spec.Params {
		switch param.Name {
		case "file":
			testutil.Require(t, param.Format == "binary" && param.ContentType == "", "file = %#v", param)
		case "note":
			testutil.Require(t, param.ContentType == "" && param.In == "formData", "note = %#v", param)
		}
	}
}
