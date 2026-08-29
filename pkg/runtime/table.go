package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// PreferredColumns is the canonical ordering used both by codegen (to derive
// per-spec DefaultColumns from response schemas) and by runtime (as a fallback
// when no hints are available). Single source of truth.
var PreferredColumns = []string{
	"metadata.name", "name",
	"metadata.namespace", "namespace",
	"status.phase", "phase",
	"status.ready",
	"kind", "type",
	"spec.provider", "spec.region",
	"status.version", "spec.version",
	"metadata.creationTimestamp", "creationTimestamp",
	"id", "uid", "uuid", "modelName",
}

const maxColumns = 6

func renderTable(data []byte, w io.Writer, hints OutputHints) error {
	var v any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&v); err != nil {
		_, werr := w.Write(data)
		return werr
	}
	rows := extractRows(v, hints.ListPath)
	if len(rows) == 0 {
		return renderJSON(data, w)
	}
	cols := chooseColumns(rows, hints)
	if len(cols) == 0 {
		return renderJSON(data, w)
	}
	return writeTable(cols, rows, hints.ColumnLabels, hints.ColumnFormats, w)
}

// chooseColumns prefers codegen-supplied DefaultColumns when available,
// keeping only columns that actually appear in the rows. Falls back to the
// heuristic pickColumns when no hint is given or all hinted columns are absent.
func chooseColumns(rows []map[string]any, hints OutputHints) []string {
	if len(hints.DefaultColumns) == 0 {
		return pickColumns(rows)
	}
	var picked []string
	for _, c := range hints.DefaultColumns {
		for _, r := range rows {
			if _, ok := tableValue(r, c); ok {
				picked = append(picked, c)
				break
			}
		}
	}
	if len(picked) == 0 {
		return pickColumns(rows)
	}
	return picked
}

// extractRows normalizes a JSON response into a list of row objects.
// listPath (when non-empty) is the codegen-identified key holding the array;
// falls back to {items:[...]} and top-level arrays.
func extractRows(v any, listPath string) []map[string]any {
	switch m := v.(type) {
	case map[string]any:
		if listPath != "" {
			if raw, ok := getNestedPath(m, listPath); ok {
				arr, ok := raw.([]any)
				if !ok {
					return nil
				}
				return itemsToRows(arr)
			}
			return nil
		}
		if items, ok := m["items"].([]any); ok {
			return itemsToRows(items)
		}
		return []map[string]any{m}
	case []any:
		return itemsToRows(m)
	}
	return nil
}

func itemsToRows(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func pickColumns(rows []map[string]any) []string {
	pathSet := map[string]struct{}{}
	for _, r := range rows {
		collectPaths(r, "", 2, pathSet)
	}
	if len(pathSet) == 0 {
		return nil
	}

	used := map[string]struct{}{}
	picked := []string{}

	for _, p := range PreferredColumns {
		if _, ok := pathSet[p]; !ok {
			continue
		}
		if _, seen := used[p]; seen {
			continue
		}
		picked = append(picked, p)
		used[p] = struct{}{}
		if len(picked) >= maxColumns {
			return picked
		}
	}

	var rest1, rest2 []string
	for p := range pathSet {
		if _, ok := used[p]; ok {
			continue
		}
		if strings.Contains(p, ".") {
			rest2 = append(rest2, p)
		} else {
			rest1 = append(rest1, p)
		}
	}
	sort.Strings(rest1)
	sort.Strings(rest2)
	picked = append(picked, rest1...)
	picked = append(picked, rest2...)
	if len(picked) > maxColumns {
		picked = picked[:maxColumns]
	}
	return picked
}

// collectPaths walks v up to maxDepth and records every scalar leaf as a dot path.
// Arrays and nulls are skipped (they don't render well as a table column).
func collectPaths(v any, prefix string, maxDepth int, out map[string]struct{}) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, vv := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch vv.(type) {
		case map[string]any:
			if strings.Count(key, ".")+1 < maxDepth {
				collectPaths(vv, key, maxDepth, out)
			}
		case []any, nil:
			// skip
		default:
			out[key] = struct{}{}
		}
	}
}

func lookupPath(row map[string]any, path string, formats map[string]ColumnFormat) string {
	v, ok := tableValue(row, path)
	if !ok {
		return ""
	}
	if format, ok := formats[path]; ok {
		if formatted, ok := formatColumnValue(v, format); ok {
			return formatted
		}
	}
	return stringify(v)
}

func tableValue(row map[string]any, path string) (any, bool) {
	var v any = row
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	if v == nil {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
		return nil, false
	default:
		return v, true
	}
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case json.Number:
		value, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return stringify(value)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case bool:
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func writeTable(cols []string, rows []map[string]any, labels map[string]string, formats map[string]ColumnFormat, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, c := range cols {
		if i > 0 {
			fmt.Fprint(tw, "\t")
		}
		fmt.Fprint(tw, columnHeader(c, labels))
	}
	fmt.Fprintln(tw)
	for _, r := range rows {
		for i, c := range cols {
			if i > 0 {
				fmt.Fprint(tw, "\t")
			}
			fmt.Fprint(tw, lookupPath(r, c, formats))
		}
		fmt.Fprintln(tw)
	}
	return tw.Flush()
}

func columnHeader(path string, labels map[string]string) string {
	if label := labels[path]; label != "" {
		return label
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return strings.ToUpper(path[i+1:])
	}
	return strings.ToUpper(path)
}
