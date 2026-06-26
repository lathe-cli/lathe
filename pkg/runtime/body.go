package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ReadBody reads a request body from a file path. If path is "-", reads from stdin.
func ReadBody(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// BuildBodyFromSet turns repeated --set key.path=value flags into a JSON
// document. Dotted keys produce nested objects. Value types are inferred:
// "true"/"false" → bool, "null" → null, integer/float strings → number,
// otherwise string. No schema validation — runtime stays schema-agnostic;
// the spec only carries a SchemaRef for future use.
func BuildBodyFromSet(sets []string) ([]byte, error) {
	return buildBodyFromSet(sets, nil)
}

func buildBodyFromSet(sets []string, stringSets []string) ([]byte, error) {
	out := map[string]any{}
	for _, kv := range sets {
		path, value, err := parseSet(kv, "--set")
		if err != nil {
			return nil, err
		}
		if err := setNestedPath(out, path, inferValue(value)); err != nil {
			return nil, err
		}
	}
	for _, kv := range stringSets {
		path, value, err := parseSet(kv, "--set-str")
		if err != nil {
			return nil, err
		}
		if err := setNestedPath(out, path, value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(out)
}

func buildEnvelopeBody(template, mergePath string, vars map[string]any, sets, stringSets []string, fileData []byte, hasFile bool) ([]byte, error) {
	var envelope map[string]any
	if err := json.Unmarshal([]byte(template), &envelope); err != nil {
		return nil, fmt.Errorf("invalid request body template: %w", err)
	}
	if hasFile {
		if mergePath == "" {
			return nil, fmt.Errorf("--file is not supported for this command's body template")
		}
		var v any
		if len(strings.TrimSpace(string(fileData))) > 0 {
			if err := json.Unmarshal(fileData, &v); err != nil {
				return nil, fmt.Errorf("invalid --file JSON: %w", err)
			}
		}
		if err := setNestedPath(envelope, mergePath, v); err != nil {
			return nil, err
		}
	}
	for name, value := range vars {
		if err := setNestedPath(envelope, joinBodyPath(mergePath, name), value); err != nil {
			return nil, err
		}
	}
	for _, kv := range sets {
		path, value, err := parseSet(kv, "--set")
		if err != nil {
			return nil, err
		}
		if err := setNestedPath(envelope, joinBodyPath(mergePath, path), inferValue(value)); err != nil {
			return nil, err
		}
	}
	for _, kv := range stringSets {
		path, value, err := parseSet(kv, "--set-str")
		if err != nil {
			return nil, err
		}
		if err := setNestedPath(envelope, joinBodyPath(mergePath, path), value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(envelope)
}

func joinBodyPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return prefix + "." + path
}

func parseSet(kv string, flag string) (string, string, error) {
	eq := strings.Index(kv, "=")
	if eq < 0 {
		return "", "", fmt.Errorf("invalid %s %q (expected key=value)", flag, kv)
	}
	path := kv[:eq]
	if path == "" {
		return "", "", fmt.Errorf("invalid %s %q (empty key)", flag, kv)
	}
	return path, kv[eq+1:], nil
}

type pathSegment struct {
	key string
	idx int // -1 = object field, >=0 = array index within key
}

func parsePath(path string) []pathSegment {
	parts := strings.Split(path, ".")
	segs := make([]pathSegment, 0, len(parts))
	for _, p := range parts {
		if open := strings.Index(p, "["); open >= 0 && strings.HasSuffix(p, "]") {
			key := p[:open]
			if idx, err := strconv.Atoi(p[open+1 : len(p)-1]); err == nil {
				segs = append(segs, pathSegment{key: key, idx: idx})
				continue
			}
		}
		segs = append(segs, pathSegment{key: p, idx: -1})
	}
	return segs
}

func setNestedPath(m map[string]any, path string, v any) error {
	return setNestedSegs(m, parsePath(path), v)
}

func getNestedPath(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	return getNestedSegs(v, parsePath(path))
}

func getNestedSegs(v any, segs []pathSegment) (any, bool) {
	if len(segs) == 0 {
		return v, true
	}
	seg := segs[0]
	rest := segs[1:]
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	next, ok := m[seg.key]
	if !ok {
		return nil, false
	}
	if seg.idx >= 0 {
		arr, ok := next.([]any)
		if !ok || seg.idx >= len(arr) {
			return nil, false
		}
		next = arr[seg.idx]
	}
	return getNestedSegs(next, rest)
}

func setNestedSegs(m map[string]any, segs []pathSegment, v any) error {
	if len(segs) == 0 {
		return nil
	}
	seg := segs[0]
	rest := segs[1:]

	if seg.idx < 0 {
		if len(rest) == 0 {
			m[seg.key] = v
			return nil
		}
		switch next := m[seg.key].(type) {
		case map[string]any:
			return setNestedSegs(next, rest, v)
		case nil:
			child := map[string]any{}
			m[seg.key] = child
			return setNestedSegs(child, rest, v)
		default:
			return fmt.Errorf("conflicting --set: %s is not an object", seg.key)
		}
	}

	var arr []any
	switch existing := m[seg.key].(type) {
	case []any:
		arr = existing
	case nil:
		arr = []any{}
	default:
		return fmt.Errorf("conflicting --set: %s is not an array", seg.key)
	}
	for len(arr) <= seg.idx {
		arr = append(arr, nil)
	}
	if len(rest) == 0 {
		arr[seg.idx] = v
	} else {
		var child map[string]any
		switch existing := arr[seg.idx].(type) {
		case map[string]any:
			child = existing
		case nil:
			child = map[string]any{}
		default:
			return fmt.Errorf("conflicting --set: %s[%d] is not an object", seg.key, seg.idx)
		}
		if err := setNestedSegs(child, rest, v); err != nil {
			return err
		}
		arr[seg.idx] = child
	}
	m[seg.key] = arr
	return nil
}

func inferValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	case "[]":
		return []any{}
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
