package openapi3

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
