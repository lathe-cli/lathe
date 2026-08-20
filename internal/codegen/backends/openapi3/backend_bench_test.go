package openapi3

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

// benchDoc builds an OpenAPI 3 document shaped like a real product API:
// every resource contributes a collection and an item path, shared component
// schemas referenced through $ref, list envelopes, pagination params, and a
// document-level security requirement.
func benchDoc(resources int) map[string]any {
	schemas := map[string]any{
		"Owner": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":    map[string]any{"type": "string"},
				"email": map[string]any{"type": "string", "format": "email"},
				"name":  map[string]any{"type": "string"},
			},
			"required": []any{"id"},
		},
		"Error": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code":    map[string]any{"type": "integer", "format": "int32"},
				"message": map[string]any{"type": "string"},
			},
		},
	}
	paths := map[string]any{}

	for i := range resources {
		name := fmt.Sprintf("Resource%d", i)
		collection := fmt.Sprintf("/v1/resource%ds", i)
		item := collection + "/{resource_id}"

		schemas[name] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"created_at":  map[string]any{"type": "string", "format": "date-time"},
				"enabled":     map[string]any{"type": "boolean"},
				"size":        map[string]any{"type": "integer", "format": "int64"},
				"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"owner":       map[string]any{"$ref": "#/components/schemas/Owner"},
				"labels": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
			},
			"required": []any{"name"},
		}
		schemas[name+"List"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items":           map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/" + name}},
				"next_page_token": map[string]any{"type": "string"},
			},
		}

		jsonResponse := func(ref string) map[string]any {
			return map[string]any{
				"200": map[string]any{
					"description": "ok",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/" + ref},
						},
					},
				},
				"default": map[string]any{
					"description": "error",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/Error"},
						},
					},
				},
			}
		}
		jsonBody := map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + name},
				},
			},
		}

		paths[collection] = map[string]any{
			"get": map[string]any{
				"operationId": name + "_List",
				"tags":        []any{name},
				"summary":     "List " + name,
				"description": "List every " + name + " visible to the caller.",
				"parameters": []any{
					map[string]any{"name": "page_size", "in": "query", "schema": map[string]any{"type": "integer", "format": "int32"}},
					map[string]any{"name": "page_token", "in": "query", "schema": map[string]any{"type": "string"}},
					map[string]any{"name": "filter", "in": "query", "schema": map[string]any{"type": "string"}},
					map[string]any{"name": "order_by", "in": "query", "schema": map[string]any{"type": "string", "enum": []any{"name", "created_at"}}},
				},
				"responses": jsonResponse(name + "List"),
			},
			"post": map[string]any{
				"operationId": name + "_Create",
				"tags":        []any{name},
				"summary":     "Create " + name,
				"requestBody": jsonBody,
				"responses":   jsonResponse(name),
			},
		}
		paths[item] = map[string]any{
			"parameters": []any{
				map[string]any{"name": "resource_id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
			},
			"get": map[string]any{
				"operationId": name + "_Get",
				"tags":        []any{name},
				"summary":     "Get " + name,
				"responses":   jsonResponse(name),
			},
			"patch": map[string]any{
				"operationId": name + "_Update",
				"tags":        []any{name},
				"summary":     "Update " + name,
				"requestBody": jsonBody,
				"responses":   jsonResponse(name),
			},
			"delete": map[string]any{
				"operationId": name + "_Delete",
				"tags":        []any{name},
				"summary":     "Delete " + name,
				"responses":   jsonResponse(name),
			},
		}
	}

	return map[string]any{
		"openapi":    "3.0.3",
		"info":       map[string]any{"title": "bench", "version": "1.0.0"},
		"servers":    []any{map[string]any{"url": "https://api.example.com/v1"}},
		"security":   []any{map[string]any{"bearerAuth": []any{}}},
		"paths":      paths,
		"components": map[string]any{"schemas": schemas},
	}
}

// benchSource writes the synthetic document in the requested encoding and
// returns the sync dir plus the source pointing at it.
func benchSource(b *testing.B, resources int, ext string) (*sourceconfig.Source, string) {
	b.Helper()
	doc := benchDoc(resources)

	var (
		data []byte
		err  error
	)
	switch ext {
	case ".yaml":
		data, err = yaml.Marshal(doc)
	default:
		data, err = json.Marshal(doc)
	}
	if err != nil {
		b.Fatalf("encode %s document: %v", ext, err)
	}

	syncDir := b.TempDir()
	file := "openapi" + ext
	if err := os.WriteFile(filepath.Join(syncDir, file), data, 0o600); err != nil {
		b.Fatalf("seed input: %v", err)
	}
	return &sourceconfig.Source{
		Name:     "bench",
		OpenAPI3: &sourceconfig.OpenAPI3Config{Files: []string{file}},
	}, syncDir
}

func BenchmarkParse(b *testing.B) {
	cases := []struct {
		name      string
		resources int
		ext       string
	}{
		{"json-small", 4, ".json"},
		{"json-large", 48, ".json"},
		{"yaml-large", 48, ".yaml"},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			src, syncDir := benchSource(b, tc.resources, tc.ext)
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

// BenchmarkParseNormalize covers the codegen-time hot path a user pays on
// every `lathe codegen`: spec parsing plus normalization into CommandSpecs.
func BenchmarkParseNormalize(b *testing.B) {
	src, syncDir := benchSource(b, 48, ".json")
	for b.Loop() {
		mod, err := Parse(src, syncDir)
		if err != nil {
			b.Fatalf("Parse: %v", err)
		}
		if specs := normalize.Normalize(mod); len(specs) == 0 {
			b.Fatal("Normalize produced no specs")
		}
	}
}
