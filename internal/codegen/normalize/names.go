package normalize

import (
	"strings"
	"unicode"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
)

func joinBasePath(basePath, operationPath string) string {
	basePath = strings.TrimRight(basePath, "/")
	if basePath == "" {
		return operationPath
	}
	return "/" + strings.TrimLeft(basePath, "/") + "/" + strings.TrimLeft(operationPath, "/")
}

func synthOperationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(path, "/") {
		seg = strings.Trim(seg, "{}")
		var s strings.Builder
		for _, r := range seg {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				s.WriteRune(r)
			}
		}
		if s.Len() == 0 {
			continue
		}
		w := s.String()
		b.WriteString(strings.ToUpper(w[:1]))
		b.WriteString(w[1:])
	}
	return b.String()
}

func synthUseName(method, path string, trim int) string {
	segs := pathSegments(path)
	if trim < len(segs) {
		segs = segs[trim:]
	}
	parts := []string{strings.ToLower(method)}
	for _, seg := range segs {
		if s := sanitizeSegment(seg); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "-")
}

func commonNoisePrefix(ops []rawir.RawOperation) int {
	var paths [][]string
	for _, op := range ops {
		if op.OperationID != "" {
			continue
		}
		paths = append(paths, pathSegments(op.Path))
	}
	if len(paths) == 0 {
		return 0
	}
	n := 0
	for {

		if n >= len(paths[0])-1 || !noiseSegment(paths[0][n]) {
			return n
		}
		for _, p := range paths[1:] {
			if n >= len(p)-1 || p[n] != paths[0][n] {
				return n
			}
		}
		n++
	}
}

func noiseSegment(seg string) bool {
	switch folded := foldToken(seg); folded {
	case "api", "apis", "rest":
		return true
	default:
		if len(folded) < 2 || folded[0] != 'v' || folded[1] < '0' || folded[1] > '9' {
			return false
		}
		return true
	}
}

func pathSegments(path string) []string {
	var out []string
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

func sanitizeSegment(seg string) string {
	seg = strings.Trim(seg, "{}")
	seg = strings.NewReplacer("_", "-", ".", "-", " ", "-").Replace(seg)
	var b strings.Builder
	for _, r := range camelToKebab(seg) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(collapseDashes(b.String()), "-")
}

func collapseDashes(s string) string {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if r == '-' {
			if prev {
				continue
			}
			prev = true
		} else {
			prev = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func group(op rawir.RawOperation) string {
	if op.Group != "" {
		return op.Group
	}
	return "Default"
}

func opNameFromID(id, group, module string) string {
	idx := strings.Index(id, "_")
	if idx <= 0 {
		return id
	}
	prefix, suffix := id[:idx], id[idx+1:]
	if repeatsIDPrefix(prefix, suffix) {
		return suffix
	}
	if sameToken(prefix, group) || sameToken(prefix, module) {
		return suffix
	}
	return id
}

func repeatsIDPrefix(prefix, suffix string) bool {
	if !strings.HasPrefix(suffix, prefix) {
		return false
	}
	for _, next := range suffix[len(prefix):] {
		return next == '_' || next == '-' || unicode.IsUpper(next) || unicode.IsDigit(next)
	}
	return true
}

func kebabFromID(id string) string {
	return strings.Trim(collapseDashes(camelToKebab(strings.ReplaceAll(id, "_", "-"))), "-")
}

func sameToken(a, b string) bool {
	fa, fb := foldToken(a), foldToken(b)
	if fa == "" || fb == "" {
		return false
	}
	return fa == fb || strings.TrimSuffix(fa, "s") == strings.TrimSuffix(fb, "s")
}

func foldToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func pickShort(op rawir.RawOperation) string {
	for _, candidate := range []string{op.Summary, op.Description} {
		s := firstLine(candidate)
		if s == "" || strings.HasPrefix(strings.ToUpper(s), "TODO") {
			continue
		}
		return s
	}
	return op.OperationID
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func camelToKebab(s string) string {
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			var next rune
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)) {
				out = append(out, '-')
			}
		}
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}
