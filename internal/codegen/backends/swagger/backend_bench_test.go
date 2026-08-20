package swagger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

// benchDoc builds a Swagger 2.0 document with one collection and one item path
// per resource, shared `definitions` reached through `$ref`, list envelopes,
// and a document-level security requirement.
func benchDoc(resources int) map[string]any {
	definitions := map[string]any{
		"Owner": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":    map[string]any{"type": "string"},
				"email": map[string]any{"type": "string"},
			},
		},
	}
	paths := map[string]any{}

	for i := range resources {
		name := fmt.Sprintf("Resource%d", i)
		definitions[name] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"enabled":     map[string]any{"type": "boolean"},
				"size":        map[string]any{"type": "integer"},
				"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"owner":       map[string]any{"$ref": "#/definitions/Owner"},
			},
			"required": []any{"name"},
		}
		definitions[name+"List"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items":           map[string]any{"type": "array", "items": map[string]any{"$ref": "#/definitions/" + name}},
				"next_page_token": map[string]any{"type": "string"},
			},
		}

		paths[fmt.Sprintf("/v1/resource%ds", i)] = map[string]any{
			"get": map[string]any{
				"operationId": name + "_List",
				"tags":        []any{name},
				"summary":     "List " + name,
				"parameters": []any{
					map[string]any{"name": "page_size", "in": "query", "type": "integer"},
					map[string]any{"name": "page_token", "in": "query", "type": "string"},
					map[string]any{"name": "order_by", "in": "query", "type": "string", "enum": []any{"name", "created_at"}},
				},
				"responses": map[string]any{
					"200": map[string]any{"schema": map[string]any{"$ref": "#/definitions/" + name + "List"}},
				},
			},
			"post": map[string]any{
				"operationId": name + "_Create",
				"tags":        []any{name},
				"summary":     "Create " + name,
				"parameters": []any{
					map[string]any{"name": "body", "in": "body", "required": true, "schema": map[string]any{"$ref": "#/definitions/" + name}},
				},
				"responses": map[string]any{
					"200": map[string]any{"schema": map[string]any{"$ref": "#/definitions/" + name}},
				},
			},
		}
		paths[fmt.Sprintf("/v1/resource%ds/{resource_id}", i)] = map[string]any{
			"get": map[string]any{
				"operationId": name + "_Get",
				"tags":        []any{name},
				"summary":     "Get " + name,
				"parameters": []any{
					map[string]any{"name": "resource_id", "in": "path", "required": true, "type": "string"},
				},
				"responses": map[string]any{
					"200": map[string]any{"schema": map[string]any{"$ref": "#/definitions/" + name}},
				},
			},
			"delete": map[string]any{
				"operationId": name + "_Delete",
				"tags":        []any{name},
				"summary":     "Delete " + name,
				"parameters": []any{
					map[string]any{"name": "resource_id", "in": "path", "required": true, "type": "string"},
				},
				"responses": map[string]any{"200": map[string]any{}},
			},
		}
	}

	return map[string]any{
		"swagger":     "2.0",
		"info":        map[string]any{"title": "bench", "version": "1.0.0"},
		"basePath":    "/",
		"produces":    []any{"application/json"},
		"security":    []any{map[string]any{"bearerAuth": []any{}}},
		"paths":       paths,
		"definitions": definitions,
	}
}

func BenchmarkParse(b *testing.B) {
	cases := []struct {
		name      string
		resources int
	}{
		{"small", 4},
		{"large", 48},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			data, err := json.Marshal(benchDoc(tc.resources))
			if err != nil {
				b.Fatalf("encode document: %v", err)
			}
			syncDir := b.TempDir()
			if err := os.WriteFile(filepath.Join(syncDir, "swagger.json"), data, 0o600); err != nil {
				b.Fatalf("seed input: %v", err)
			}
			src := &sourceconfig.Source{
				Name:    "bench",
				Swagger: &sourceconfig.SwaggerConfig{Files: []string{"swagger.json"}},
			}

			for b.Loop() {
				mod, err := Parse(src, syncDir)
				if err != nil {
					b.Fatalf("Parse: %v", err)
				}
				if len(mod.Operations) == 0 {
					b.Fatal("Parse produced no operations")
				}
			}
		})
	}
}
