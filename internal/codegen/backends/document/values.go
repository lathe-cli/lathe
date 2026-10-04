package document

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

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

type SecurityScheme struct {
	Type   string `json:"type" yaml:"type"`
	Scheme string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	In     string `json:"in,omitempty" yaml:"in,omitempty"`
	Name   string `json:"name,omitempty" yaml:"name,omitempty"`
}

func Security(requirements []map[string][]string, schemes map[string]SecurityScheme) []rawir.RawSecurityReq {
	if requirements == nil {
		return nil
	}
	out := make([]rawir.RawSecurityReq, 0, len(requirements))
	for _, requirement := range requirements {
		var raw rawir.RawSecurityReq
		for _, name := range slices.Sorted(maps.Keys(requirement)) {
			def := schemes[name]
			typ := def.Type
			scheme := strings.ToLower(def.Scheme)
			if strings.EqualFold(typ, "basic") {
				typ = "http"
				scheme = "basic"
			}
			raw.Schemes = append(raw.Schemes, rawir.RawSecurityScheme{
				Name:   name,
				Type:   typ,
				Scheme: scheme,
				In:     def.In,
				Param:  def.Name,
				Scopes: append([]string(nil), requirement[name]...),
			})
		}
		out = append(out, raw)
	}
	return out
}

func MergeNamed[T any](dst, add map[string]T, kind, module, origin string) {
	for k, v := range add {
		if existing, exists := dst[k]; exists {
			if !EqualJSON(existing, v) {
				fmt.Fprintf(os.Stderr, "warn: %s: diverging %s %q in %s (kept first)\n", module, kind, k, origin)
			}
			continue
		}
		dst[k] = v
	}
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
			if dst[i].Description == "" {
				dst[i].Description = tag.Description
			} else if tag.Description != "" && dst[i].Description != tag.Description {
				fmt.Fprintf(os.Stderr, "warn: %s: diverging tag %q in %s (kept first)\n", module, tag.Name, origin)
			}
			continue
		}
		indices[tag.Name] = len(dst)
		dst = append(dst, tag)
	}
	return dst
}
