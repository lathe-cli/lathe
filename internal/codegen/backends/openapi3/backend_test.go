package openapi3

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/runtime"
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

func TestParse_Golden(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ext   string
	}{
		{"petstore-min", petstoreMinJSON, ".json"},
		{"ref-resolution", refResolutionJSON, ".json"},
		{"path-and-query-params", pathAndQueryJSON, ".json"},
		{"request-body", requestBodyJSON, ".json"},
		{"request-body-non-json", requestBodyNonJSON, ".json"},
		{"path-level-params", pathLevelParamsJSON, ".json"},
		{"yaml-input", petstoreMinYAML, ".yaml"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			syncDir := t.TempDir()
			inputPath := filepath.Join(syncDir, tc.name+tc.ext)
			if err := os.WriteFile(inputPath, []byte(tc.input), 0o644); err != nil {
				t.Fatalf("seed input: %v", err)
			}

			src := &sourceconfig.Source{
				Name: "demo",
				OpenAPI3: &sourceconfig.OpenAPI3Config{
					Files: []string{tc.name + tc.ext},
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

func TestParse_OpenAPI31NullableTypeArray(t *testing.T) {
	syncDir := t.TempDir()
	inputPath := filepath.Join(syncDir, "openapi.json")
	if err := os.WriteFile(inputPath, []byte(openapi31NullableTypeArrayJSON), 0o644); err != nil {
		t.Fatalf("seed input: %v", err)
	}

	src := &sourceconfig.Source{
		Name: "demo",
		OpenAPI3: &sourceconfig.OpenAPI3Config{
			Files: []string{"openapi.json"},
		},
	}
	mod, err := Parse(src, syncDir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := mod.Operations[0].Parameters[0].Type; got != "boolean" {
		t.Fatalf("parameter type = %q, want boolean", got)
	}
	if got := mod.Operations[0].Responses["200"].Schema.Properties["name"].Type; got != "string" {
		t.Fatalf("property type = %q, want string", got)
	}
	if !mod.Operations[0].Responses["200"].Schema.Properties["name"].Nullable {
		t.Fatal("nullable type array lost nullability")
	}
}

func TestParse_ExposeOperationIDs(t *testing.T) {
	syncDir := t.TempDir()
	input := `{
  "openapi": "3.0.3",
  "paths": {
    "/pets": {"get": {"operationId": "Pet_List", "responses": {"200": {}}}},
    "/pets/{id}": {"get": {"operationId": "Pet_Get", "responses": {"200": {}}}},
    "/health": {"get": {"operationId": "Health_Get", "responses": {"200": {}}}}
  }
}`
	if err := os.WriteFile(filepath.Join(syncDir, "openapi.json"), []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &sourceconfig.Source{
		Name: "demo",
		OpenAPI3: &sourceconfig.OpenAPI3Config{
			Files:  []string{"openapi.json"},
			Expose: &sourceconfig.OpenAPIExpose{OperationIDs: []string{"Pet_Get", "Pet_List"}},
		},
	}
	mod, err := Parse(src, syncDir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := map[string]bool{}
	for _, op := range mod.Operations {
		got[op.OperationID] = true
	}
	if !reflect.DeepEqual(got, map[string]bool{"Pet_Get": true, "Pet_List": true}) {
		t.Fatalf("operations = %#v", got)
	}
}

func TestParse_ExposeOperationIDsFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"unmatched", `{
  "openapi": "3.0.3",
  "paths": {"/pets": {"get": {"operationId": "Pet_List", "responses": {"200": {}}}}}
}`, "matched no operations"},
		{"ambiguous", `{
  "openapi": "3.0.3",
  "paths": {"/pets": {"get": {"operationId": "Pet_Get", "responses": {"200": {}}}}}
}`, "ambiguous"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			syncDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(syncDir, "openapi.json"), []byte(tc.input), 0o644); err != nil {
				t.Fatal(err)
			}
			files := []string{"openapi.json"}
			if tc.name == "ambiguous" {
				if err := os.WriteFile(filepath.Join(syncDir, "duplicate.json"), []byte(tc.input), 0o644); err != nil {
					t.Fatal(err)
				}
				files = append(files, "duplicate.json")
			}
			src := &sourceconfig.Source{
				Name: "demo",
				OpenAPI3: &sourceconfig.OpenAPI3Config{
					Files:  files,
					Expose: &sourceconfig.OpenAPIExpose{OperationIDs: []string{"Pet_Get"}},
				},
			}
			if _, err := Parse(src, syncDir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v", err)
			}
		})
	}
}

func TestParse_OpenAPI31SchemaFidelity(t *testing.T) {
	input := `{
  "openapi": "3.1.0",
  "paths": {
    "/widgets": {
      "post": {
        "operationId": "Widget_Create",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "nickname": {"anyOf": [{"type": "string"}, {"type": "null"}]},
                  "choice": {"oneOf": [{"type": "string"}, {"type": "integer"}]},
                  "exclusive_null": {"oneOf": [{"type": ["string", "null"]}, {"type": "null"}]},
                  "metadata": {"type": "object", "additionalProperties": {"type": "string"}},
                  "freeform": {"type": "object", "additionalProperties": true},
                  "closed": {"type": "object", "additionalProperties": false},
                  "related": {"anyOf": [{"$ref": "#/components/schemas/Base"}, {"type": "null"}]},
                  "extended": {
                    "$ref": "#/components/schemas/Base",
                    "properties": {"id": {"type": "string", "nullable": true}}
                  },
                  "composed": {"allOf": [
                    {"type": "object", "properties": {"id": {"type": "string"}}},
                    {"type": "object", "properties": {"name": {"type": "string"}}}
                  ]}
                }
              }
            }
          }
        },
        "responses": {"201": {}}
      }
    }
  },
  "components": {
    "schemas": {
      "Base": {"type": "object", "properties": {"id": {"type": "string"}}}
    }
  }
}`
	schema := parseNormalized(t, input)[0].RequestBody.Schema
	if nickname := schema.Properties["nickname"]; nickname.Type != "string" || !nickname.Nullable {
		t.Fatalf("nickname = %#v, want nullable string", nickname)
	}
	if choice := schema.Properties["choice"]; len(choice.OneOf) != 2 || choice.OneOf[0].Type != "string" || choice.OneOf[1].Type != "integer" {
		t.Fatalf("choice = %#v, want string/integer oneOf", choice)
	}
	if exclusive := schema.Properties["exclusive_null"]; len(exclusive.OneOf) != 2 || !exclusive.OneOf[0].Nullable || exclusive.OneOf[1].Type != "null" {
		t.Fatalf("exclusive_null = %#v, want both oneOf branches", exclusive)
	}
	metadata := schema.Properties["metadata"].AdditionalProperties
	if metadata == nil || metadata.Schema == nil || metadata.Schema.Type != "string" {
		t.Fatalf("metadata additionalProperties = %#v, want string schema", metadata)
	}
	if freeform := schema.Properties["freeform"].AdditionalProperties; freeform == nil || !freeform.Allowed || freeform.Schema != nil {
		t.Fatalf("freeform additionalProperties = %#v, want true", freeform)
	}
	if closed := schema.Properties["closed"].AdditionalProperties; closed == nil || closed.Allowed || closed.Schema != nil {
		t.Fatalf("closed additionalProperties = %#v, want false", closed)
	}
	if related := schema.Properties["related"]; !related.Nullable || related.Properties["id"] == nil || related.Properties["id"].Type != "string" {
		t.Fatalf("related = %#v, want expanded nullable Base", related)
	}
	extended := schema.Properties["extended"]
	if extended == nil || len(extended.AllOf) != 2 || extended.AllOf[0] == nil || extended.AllOf[1] == nil || extended.AllOf[0].Properties["id"] == nil || extended.AllOf[0].Properties["id"].Nullable || extended.AllOf[1].Properties["id"] == nil || !extended.AllOf[1].Properties["id"].Nullable {
		t.Fatalf("extended = %#v, want referenced and sibling constraints", extended)
	}
	if composed := schema.Properties["composed"]; len(composed.AllOf) != 2 || composed.AllOf[0].Properties["id"].Type != "string" || composed.AllOf[1].Properties["name"].Type != "string" {
		t.Fatalf("composed = %#v, want both allOf branches", composed)
	}
}

func TestParse_OpenAPI30IgnoresSchemaReferenceSiblings(t *testing.T) {
	input := `{
  "openapi": "3.0.3",
  "paths": {
    "/widgets": {
      "post": {
        "operationId": "Widget_Create",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "$ref": "#/components/schemas/Widget",
                "type": "string"
              }
            }
          }
        },
        "responses": {"201": {}}
      }
    }
  },
  "components": {
    "schemas": {
      "Widget": {
        "type": "object",
        "properties": {"id": {"type": "string"}}
      }
    }
  }
}`
	schema := parseNormalized(t, input)[0].RequestBody.Schema
	if schema.Type != "object" || schema.Properties["id"] == nil || len(schema.AllOf) != 0 {
		t.Fatalf("schema = %#v, want referenced Widget without ignored sibling", schema)
	}
}

func TestParse_OpenAPI30NullableRequiresLocalType(t *testing.T) {
	input := `{
  "openapi": "3.0.3",
  "paths": {
    "/widgets": {
      "post": {
        "operationId": "Widget_Create",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "nullable": true,
                "allOf": [{"type": "object"}]
              }
            }
          }
        },
        "responses": {"201": {}}
      }
    }
  }
}`
	schema := parseNormalized(t, input)[0].RequestBody.Schema
	if schema.Nullable || len(schema.AllOf) != 1 || schema.AllOf[0].Type != "object" {
		t.Fatalf("schema = %#v, want non-null composed object", schema)
	}
}

func TestSchemaReferenceSiblingsAllowed(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{version: "3.0.3"},
		{version: "3.1.0", want: true},
		{version: "3.2.0", want: true},
		{version: "2.0"},
		{version: ""},
	}
	for _, tc := range tests {
		if got := schemaReferenceSiblingsAllowed(tc.version); got != tc.want {
			t.Errorf("schemaReferenceSiblingsAllowed(%q) = %t, want %t", tc.version, got, tc.want)
		}
	}
}

func TestParse_MultipartBodyFields(t *testing.T) {
	input := `{
  "openapi": "3.0.3",
  "paths": {
    "/uploads": {
      "post": {
        "operationId": "Upload_Create",
        "parameters": [
          {"name": "purpose", "in": "query", "schema": {"type": "string"}}
        ],
        "requestBody": {
          "required": true,
          "content": {
            "multipart/form-data": {
              "schema": {"$ref": "#/components/schemas/UploadForm"}
            }
          }
        },
        "responses": {"201": {}}
      }
    }
  },
  "components": {
    "schemas": {
      "UploadForm": {
        "type": "object",
        "required": ["file"],
        "properties": {
          "file": {"type": "string", "format": "binary"},
          "purpose": {"type": "string"}
        }
      }
    }
  }
}`
	spec := parseNormalized(t, input)[0]
	if spec.RequestBody == nil || spec.RequestBody.MediaType != "multipart/form-data" {
		t.Fatalf("request body = %#v", spec.RequestBody)
	}
	want := map[string]runtime.ParamSpec{
		"formData:file":    {Name: "file", Flag: "file", In: runtime.InFormData, GoType: "string", Help: "file (formData, required, binary, local file path)", Required: true, Format: "binary"},
		"formData:purpose": {Name: "purpose", Flag: "body-purpose", In: runtime.InFormData, GoType: "string", Help: "purpose (formData)"},
		"query:purpose":    {Name: "purpose", Flag: "purpose", In: runtime.InQuery, GoType: "string", Help: "purpose (query)"},
	}
	if len(spec.Params) != len(want) {
		t.Fatalf("params = %#v", spec.Params)
	}
	for _, param := range spec.Params {
		key := param.In + ":" + param.Name
		if !reflect.DeepEqual(param, want[key]) {
			t.Errorf("param %q = %#v, want %#v", key, param, want[key])
		}
	}
}

func TestParse_ResponseContentSelectionIsDeterministic(t *testing.T) {
	syncDir := t.TempDir()
	input := `{
  "openapi": "3.0.3",
  "paths": {
    "/exports": {
      "get": {
        "operationId": "Export_Get",
        "responses": {
          "201": {
            "content": {
              "text/plain": {"schema": {"type": "string"}},
              "application/xml": {
                "schema": {"type": "object", "properties": {"id": {"type": "string"}}}
              }
            }
          }
        }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(syncDir, "openapi.json"), []byte(input), 0o644); err != nil {
		t.Fatalf("seed input: %v", err)
	}
	src := &sourceconfig.Source{
		Name:     "demo",
		OpenAPI3: &sourceconfig.OpenAPI3Config{Files: []string{"openapi.json"}},
	}

	for i := 0; i < 100; i++ {
		mod, err := Parse(src, syncDir)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		response := mod.Operations[0].Responses["201"]
		if response.MediaType != "application/xml" {
			t.Fatalf("iteration %d: media type = %q, want application/xml", i, response.MediaType)
		}
		if response.Schema == nil || response.Schema.Type != "object" || response.Schema.Properties["id"] == nil {
			t.Fatalf("iteration %d: schema = %#v, want application/xml schema", i, response.Schema)
		}
	}
}

func TestParse_ServerBasePathCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		server        string
		variables     string
		wantPath      string
		wantUse       string
		wantOperation string
	}{
		{
			name:          "templated absolute URL",
			server:        "https://{environment}.example.com/api/{version}",
			variables:     `,"variables":{"environment":{"default":"prod"},"version":{"default":"v3"}}`,
			wantPath:      "/api/v3/widgets",
			wantUse:       "get-widgets",
			wantOperation: "getWidgets",
		},
		{
			name:          "ambiguous scheme-less URL",
			server:        "api.example.com/v1",
			wantPath:      "/widgets",
			wantUse:       "get-widgets",
			wantOperation: "getWidgets",
		},
		{
			name:          "encoded path separator",
			server:        "https://example.com/api%2Fv1",
			wantPath:      "/api%2Fv1/widgets",
			wantUse:       "get-widgets",
			wantOperation: "getWidgets",
		},
		{
			name:          "encoded query delimiter",
			server:        "/api%3Ftenant",
			wantPath:      "/api%3Ftenant/widgets",
			wantUse:       "get-widgets",
			wantOperation: "getWidgets",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{
  "openapi": "3.0.3",
  "servers": [{"url": %q%s}],
  "paths": {
    "/widgets": {
      "get": {"responses": {"200": {}}}
    }
  }
}`, tt.server, tt.variables)
			specs := parseNormalized(t, input)
			if got := specs[0].PathTpl; got != tt.wantPath {
				t.Fatalf("path = %q, want %q", got, tt.wantPath)
			}
			if got := specs[0].Use; got != tt.wantUse {
				t.Fatalf("use = %q, want %q", got, tt.wantUse)
			}
			if got := specs[0].OperationID; got != tt.wantOperation {
				t.Fatalf("operation id = %q, want %q", got, tt.wantOperation)
			}
		})
	}
}

func TestParse_ServerOverridePrecedence(t *testing.T) {
	input := `{
  "openapi": "3.0.3",
  "servers": [{"url": "/api/v1"}],
  "paths": {
    "/root": {
      "get": {"operationId": "Root_Get", "responses": {"200": {}}}
    },
    "/health": {
      "servers": [{"url": "/"}],
      "get": {"operationId": "Health_Get", "responses": {"200": {}}}
    },
    "/widgets": {
      "servers": [{"url": "/api/v2"}],
      "get": {
        "operationId": "Widget_Get",
        "servers": [{"url": "/api/v3"}],
        "responses": {"200": {}}
      },
      "post": {"operationId": "Widget_Create", "responses": {"200": {}}}
    }
  }
}`
	want := map[string]string{
		"Root_Get":      "/api/v1/root",
		"Health_Get":    "/health",
		"Widget_Get":    "/api/v3/widgets",
		"Widget_Create": "/api/v2/widgets",
	}
	for _, spec := range parseNormalized(t, input) {
		if got := spec.PathTpl; got != want[spec.OperationID] {
			t.Errorf("%s path = %q, want %q", spec.OperationID, got, want[spec.OperationID])
		}
		delete(want, spec.OperationID)
	}
	if len(want) != 0 {
		t.Fatalf("missing operations: %v", want)
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
			input := `{"openapi":"3.0.3"` + tc.documentSecurity + `,"paths":{"/health":{"get":{"operationId":"Health_Get"` + tc.operationSecurity + `,"responses":{"200":{}}}}}}`
			security := parseNormalized(t, input)[0].Security
			if security == nil || security.Public != tc.wantPublic || !reflect.DeepEqual(security.Scopes, tc.wantScopes) {
				t.Fatalf("security = %#v, want public=%t scopes=%v", security, tc.wantPublic, tc.wantScopes)
			}
		})
	}
}

func parseNormalized(t *testing.T, input string) []runtime.CommandSpec {
	t.Helper()
	syncDir := t.TempDir()
	inputPath := filepath.Join(syncDir, "openapi.json")
	if err := os.WriteFile(inputPath, []byte(input), 0o644); err != nil {
		t.Fatalf("seed input: %v", err)
	}

	src := &sourceconfig.Source{
		Name: "demo",
		OpenAPI3: &sourceconfig.OpenAPI3Config{
			Files: []string{"openapi.json"},
		},
	}
	mod, err := Parse(src, syncDir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	specs := normalize.Normalize(mod)
	if len(specs) == 0 {
		t.Fatal("Normalize produced no commands")
	}
	return specs
}

func TestParse_RejectsOpenAPI31MultiTypeUnion(t *testing.T) {
	syncDir := t.TempDir()
	inputPath := filepath.Join(syncDir, "openapi.json")
	if err := os.WriteFile(inputPath, []byte(openapi31MultiTypeUnionJSON), 0o644); err != nil {
		t.Fatalf("seed input: %v", err)
	}

	src := &sourceconfig.Source{
		Name: "demo",
		OpenAPI3: &sourceconfig.OpenAPI3Config{
			Files: []string{"openapi.json"},
		},
	}
	_, err := Parse(src, syncDir)
	if err == nil {
		t.Fatal("Parse succeeded, want unsupported union error")
	}
	if !strings.Contains(err.Error(), "unsupported schema type union") {
		t.Fatalf("error = %v, want unsupported schema type union", err)
	}
}

const petstoreMinJSON = `{
  "openapi": "3.0.3",
  "paths": {
    "/pets": {
      "get": {
        "operationId": "Pet_List",
        "tags": ["Pets"],
        "summary": "List pets.",
        "responses": {
          "200": {
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {"$ref": "#/components/schemas/Pet"}
                }
              }
            }
          }
        }
      }
    },
    "/pets/{id}": {
      "get": {
        "operationId": "Pet_Get",
        "tags": ["Pets"],
        "summary": "Get one pet.",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {
          "200": {
            "content": {
              "application/json": {
                "schema": {"$ref": "#/components/schemas/Pet"}
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Pet": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      }
    }
  }
}`

const refResolutionJSON = `{
  "openapi": "3.0.3",
  "paths": {
    "/pets": {
      "post": {
        "operationId": "Pet_Create",
        "tags": ["Pets"],
        "summary": "Create a pet.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/Pet"}
            }
          }
        },
        "responses": {
          "200": {
            "content": {
              "application/json": {
                "schema": {"$ref": "#/components/schemas/Pet"}
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Pet": {
        "type": "object",
        "properties": {
          "name": {"type": "string"}
        }
      }
    }
  }
}`

const pathAndQueryJSON = `{
  "openapi": "3.0.3",
  "paths": {
    "/users/{id}": {
      "get": {
        "operationId": "User_Get",
        "tags": ["Users"],
        "summary": "Get a user.",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer"}, "description": "Max rows."}
        ],
        "responses": {}
      }
    }
  }
}`

const requestBodyJSON = `{
  "openapi": "3.0.3",
  "paths": {
    "/users": {
      "post": {
        "operationId": "User_Create",
        "tags": ["Users"],
        "summary": "Create a user.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "email": {"type": "string"},
                  "role": {"type": "string"}
                }
              }
            }
          }
        },
        "responses": {
          "201": {
            "content": {
              "application/json": {
                "schema": {"$ref": "#/components/schemas/User"}
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "User": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "email": {"type": "string"},
          "role": {"type": "string"}
        }
      }
    }
  }
}`

const requestBodyNonJSON = `{
  "openapi": "3.0.3",
  "paths": {
    "/exports": {
      "post": {
        "operationId": "Export_Create",
        "tags": ["Exports"],
        "summary": "Create export.",
        "requestBody": {
          "required": true,
          "content": {
            "text/plain": {"schema": {"type": "string"}},
            "application/xml": {"schema": {"type": "object", "properties": {"id": {"type": "string"}}}}
          }
        },
        "responses": {}
      }
    }
  }
}`

const pathLevelParamsJSON = `{
  "openapi": "3.0.3",
  "paths": {
    "/orgs/{org_id}/members": {
      "parameters": [
        {"name": "org_id", "in": "path", "required": true, "schema": {"type": "string"}}
      ],
      "get": {
        "operationId": "Org_ListMembers",
        "tags": ["Orgs"],
        "summary": "List org members.",
        "parameters": [
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer"}}
        ],
        "responses": {}
      },
      "post": {
        "operationId": "Org_AddMember",
        "tags": ["Orgs"],
        "summary": "Add a member.",
        "parameters": [
          {"name": "org_id", "in": "path", "required": true, "schema": {"type": "string"}, "description": "Override"}
        ],
        "requestBody": {"required": true},
        "responses": {}
      }
    }
  }
}`

const openapi31NullableTypeArrayJSON = `{
  "openapi": "3.1.0",
  "paths": {
    "/threads": {
      "get": {
        "operationId": "Thread_List",
        "tags": ["Threads"],
        "parameters": [
          {"name": "archived", "in": "query", "schema": {"type": ["boolean", "null"]}}
        ],
        "responses": {
          "200": {
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "name": {"type": ["string", "null"]}
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

const openapi31MultiTypeUnionJSON = `{
  "openapi": "3.1.0",
  "paths": {
    "/threads": {
      "get": {
        "operationId": "Thread_List",
        "tags": ["Threads"],
        "parameters": [
          {"name": "filter", "in": "query", "schema": {"type": ["string", "integer"]}}
        ],
        "responses": {}
      }
    }
  }
}`

const petstoreMinYAML = `openapi: "3.0.3"
paths:
  /pets:
    get:
      operationId: Pet_List
      tags: [Pets]
      summary: List pets.
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Pet"
components:
  schemas:
    Pet:
      type: object
      properties:
        id:
          type: integer
        name:
          type: string
`
