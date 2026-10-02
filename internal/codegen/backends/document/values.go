package document

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
)

func String(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func Strings(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = fmt.Sprint(value)
	}
	return out
}

func EqualJSON(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func Security(requirements []map[string][]string) []rawir.RawSecurityReq {
	if requirements == nil {
		return nil
	}
	out := make([]rawir.RawSecurityReq, 0, len(requirements))
	for _, requirement := range requirements {
		var scopes []string
		for _, values := range requirement {
			scopes = append(scopes, values...)
		}
		out = append(out, rawir.RawSecurityReq{Scopes: scopes})
	}
	return out
}

type Tag struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
}

func MergeTags(dst, add []Tag, module, origin string) []Tag {
	indices := make(map[string]int, len(dst))
	for i, tag := range dst {
		indices[tag.Name] = i
	}
	for _, tag := range add {
		if i, exists := indices[tag.Name]; exists {
			if dst[i].Description != tag.Description {
				fmt.Fprintf(os.Stderr, "warn: %s: diverging tag %q in %s (kept first)\n", module, tag.Name, origin)
			}
			continue
		}
		indices[tag.Name] = len(dst)
		dst = append(dst, tag)
	}
	return dst
}
