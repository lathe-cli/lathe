package openapi3

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
