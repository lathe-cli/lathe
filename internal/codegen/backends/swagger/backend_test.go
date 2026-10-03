package swagger

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

func TestParse_CollectionFormat(t *testing.T) {
	mod := parseInput(t, `{
	  "swagger": "2.0",
	  "paths": {
	    "/items/{pathCsv}": {
	      "get": {
	        "operationId": "Items_List",
	        "parameters": [
	          {"name": "csvDefault", "in": "query", "type": "array", "items": {"type": "string"}},
	          {"name": "csv", "in": "query", "type": "array", "items": {"type": "string"}, "collectionFormat": "csv"},
	          {"name": "multi", "in": "query", "type": "array", "items": {"type": "string"}, "collectionFormat": "multi"},
	          {"name": "ssv", "in": "query", "type": "array", "items": {"type": "string"}, "collectionFormat": "ssv"},
	          {"name": "pipes", "in": "query", "type": "array", "items": {"type": "string"}, "collectionFormat": "pipes"},
	          {"name": "tsv", "in": "query", "type": "array", "items": {"type": "string"}, "collectionFormat": "tsv"},
	          {"name": "pathCsv", "in": "path", "required": true, "type": "array", "items": {"type": "string"}},
	          {"name": "pathMulti", "in": "path", "required": true, "type": "array", "items": {"type": "string"}, "collectionFormat": "multi"},
	          {"name": "headerSsv", "in": "header", "type": "array", "items": {"type": "string"}, "collectionFormat": "ssv"},
	          {"name": "formCsv", "in": "formData", "type": "array", "items": {"type": "string"}}
	        ],
	        "responses": {"200": {}}
	      }
	    }
	  }
	}`, ".json")
	got := map[string]rawir.RawParameter{}
	for _, param := range mod.Operations[0].Parameters {
		got[param.Name] = param
	}
	checkCollection := func(name, style string, explode bool) {
		t.Helper()
		param := got[name]
		testutil.Check(t, param.Style == style && param.Explode != nil && *param.Explode == explode, "%s = %+v explode=%v", name, param, param.Explode)
	}
	checkCollection("csvDefault", "form", false)
	checkCollection("csv", "form", false)
	checkCollection("multi", "form", true)
	checkCollection("ssv", "spaceDelimited", false)
	checkCollection("pipes", "pipeDelimited", false)
	checkCollection("tsv", "tabDelimited", false)
	checkCollection("pathCsv", "simple", false)
	checkCollection("pathMulti", "form", true)
	header := got["headerSsv"]
	testutil.Check(t, header.In == "header" && header.Style == "" && header.Explode == nil, "header = %+v", header)
	form := got["formCsv"]
	testutil.Check(t, form.In == "formData" && form.Style == "" && form.Explode == nil, "formData = %+v", form)
}

func TestParse_HeaderArraysPassThroughAndPathNamesCollectionFormat(t *testing.T) {
	mod := parseInput(t, `{
	  "swagger": "2.0",
	  "paths": {
	    "/items/{pathSsv}/{pathPipes}": {
	      "get": {
	        "operationId": "Items_List",
	        "parameters": [
	          {"name": "headerSsv", "in": "header", "type": "array", "items": {"type": "string"}, "collectionFormat": "ssv"},
	          {"name": "headerPipes", "in": "header", "type": "array", "items": {"type": "string"}, "collectionFormat": "pipes"},
	          {"name": "pathSsv", "in": "path", "required": true, "type": "array", "items": {"type": "string"}, "collectionFormat": "ssv"},
	          {"name": "pathPipes", "in": "path", "required": true, "type": "array", "items": {"type": "string"}, "collectionFormat": "pipes"}
	        ],
	        "responses": {"200": {}}
	      }
	    }
	  }
	}`, ".json")
	got := map[string]rawir.RawParameter{}
	for _, param := range mod.Operations[0].Parameters {
		got[param.Name] = param
	}
	for _, name := range []string{"headerSsv", "headerPipes"} {
		param := got[name]
		testutil.Check(t, param.Style == "" && param.Explode == nil, "%s = %+v", name, param)
	}
	testutil.Check(t, got["pathSsv"].Style == "ssv" && got["pathPipes"].Style == "pipes", "path styles = %q %q", got["pathSsv"].Style, got["pathPipes"].Style)
	specs := normalize.Normalize(mod)
	testutil.Require(t, len(specs) == 1, "specs = %d", len(specs))
	for _, param := range specs[0].Params {
		if param.In == "header" {
			testutil.Check(t, param.GoType == "string" && param.Style == "", "header param = %+v", param)
		}
	}
	problems := normalize.ValidateParameters(mod)
	testutil.Require(t, len(problems) == 2, "problems = %+v", problems)
	seen := map[string]bool{}
	for _, problem := range problems {
		seen[problem.Style] = true
		msg := problem.Error()
		testutil.Check(t, strings.Contains(msg, problem.Style) && !strings.Contains(msg, "spaceDelimited") && !strings.Contains(msg, "pipeDelimited"), "error = %q", msg)
	}
	testutil.Check(t, seen["ssv"] && seen["pipes"], "styles = %v", seen)
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
