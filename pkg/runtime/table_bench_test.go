package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"
)

// benchListPayload builds a paginated list response of the shape generated
// commands print after every successful list call.
func benchListPayload(b *testing.B, rows int) []byte {
	b.Helper()
	items := make([]map[string]any, 0, rows)
	for i := range rows {
		items = append(items, map[string]any{
			"id":          fmt.Sprintf("res-%06d", i),
			"name":        fmt.Sprintf("resource-%d", i),
			"description": "a synthetic resource used to exercise output rendering",
			"enabled":     i%2 == 0,
			"size":        i * 1024,
			"created_at":  "2024-05-06T07:08:09Z",
			"tags":        []string{"alpha", "beta"},
			"owner":       map[string]any{"id": fmt.Sprintf("owner-%d", i%16), "email": "owner@example.com"},
		})
	}
	data, err := json.Marshal(map[string]any{"items": items, "next_page_token": "eyJvZmZzZXQiOjEwMH0="})
	if err != nil {
		b.Fatalf("encode payload: %v", err)
	}
	return data
}

// BenchmarkFormat measures response rendering, which runs on every command
// invocation that prints a result.
func BenchmarkFormat(b *testing.B) {
	data := benchListPayload(b, 200)
	hints := OutputHints{
		ListPath:          "items",
		DefaultColumns:    []string{"id", "name", "enabled", "size"},
		ResponseMediaType: "application/json",
	}

	for _, name := range []string{"table", "json", "yaml"} {
		formatter, ok := formatters[name]
		if !ok {
			b.Fatalf("formatter %q is not registered", name)
		}
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if err := formatter.Format(io.Discard, data, hints); err != nil {
					b.Fatalf("Format: %v", err)
				}
			}
		})
	}
}

// BenchmarkFormatTableInferredColumns measures the column inference path used
// when codegen supplied no default columns.
func BenchmarkFormatTableInferredColumns(b *testing.B) {
	data := benchListPayload(b, 200)
	hints := OutputHints{ListPath: "items", ResponseMediaType: "application/json"}
	formatter := formatters["table"]
	for b.Loop() {
		if err := formatter.Format(io.Discard, data, hints); err != nil {
			b.Fatalf("Format: %v", err)
		}
	}
}
