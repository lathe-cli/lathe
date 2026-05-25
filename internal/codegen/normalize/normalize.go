package normalize

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func Normalize(mod *rawir.RawModule) []runtime.CommandSpec {
	paths := collectPaths(mod)
	sort.Strings(paths)
	byPath := groupByPath(mod)

	var specs []runtime.CommandSpec
	for _, p := range paths {
		ops := byPath[p]
		for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			op, ok := ops[m]
			if !ok || op.OperationID == "" {
				continue
			}
			useName := camelToKebab(opNameFromID(op.OperationID))
			if useName == "" {
				continue
			}
			spec := runtime.CommandSpec{
				Group:       group(op),
				Use:         useName,
				Short:       pickShort(op),
				OperationID: op.OperationID,
				Method:      op.Method,
				PathTpl:     op.Path,
			}
			for _, pp := range op.Parameters {
				switch pp.In {
				case "path":
					spec.Params = append(spec.Params, pathParam(pp))
				case "query":
					spec.Params = append(spec.Params, queryParam(pp))
				case "header":
					spec.Params = append(spec.Params, headerParam(pp))
				case "formData":
					spec.Params = append(spec.Params, formDataParam(pp))
				}
			}
			if op.RequestBody != nil {
				spec.RequestBody = &runtime.RequestBody{Required: op.RequestBody.Required, MediaType: op.RequestBody.MediaType, Schema: runtimeSchema(op.RequestBody.Schema, mod.Schemas, map[string]bool{})}
			}
			lp, itemRef := deriveList(op, mod.Schemas)
			spec.Output.ListPath = lp
			if itemRef != "" {
				spec.Output.DefaultColumns = defaultColumns(itemRef, mod.Schemas)
			}
			spec.Output.ResponseMediaType = deriveResponseMediaType(op)
			spec.Output.Pagination = derivePagination(op)
			spec.Output.Streaming = deriveStreaming(op)
			spec.Security = deriveSecurity(op)
			specs = append(specs, spec)
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Group != specs[j].Group {
			return specs[i].Group < specs[j].Group
		}
		return specs[i].Use < specs[j].Use
	})
	return specs
}

func collectPaths(mod *rawir.RawModule) []string {
	seen := map[string]bool{}
	for _, op := range mod.Operations {
		seen[op.Path] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

func groupByPath(mod *rawir.RawModule) map[string]map[string]rawir.RawOperation {
	out := map[string]map[string]rawir.RawOperation{}
	for _, op := range mod.Operations {
		bucket, ok := out[op.Path]
		if !ok {
			bucket = map[string]rawir.RawOperation{}
			out[op.Path] = bucket
		}
		bucket[op.Method] = op
	}
	return out
}

func group(op rawir.RawOperation) string {
	if op.Group != "" {
		return op.Group
	}
	return "Default"
}

func opNameFromID(id string) string {
	if idx := strings.Index(id, "_"); idx >= 0 {
		return id[idx+1:]
	}
	return id
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

func pathParam(p rawir.RawParameter) runtime.ParamSpec {
	return runtime.ParamSpec{
		Name:       p.Name,
		Flag:       camelToKebab(p.Name),
		In:         "path",
		GoType:     "string",
		Help:       helpText(p),
		Required:   true,
		Default:    p.Default,
		Enum:       p.Enum,
		Format:     p.Format,
		Deprecated: p.Deprecated,
	}
}

func queryParam(p rawir.RawParameter) runtime.ParamSpec {
	ps := runtime.ParamSpec{
		Name:       p.Name,
		Flag:       camelToKebab(p.Name),
		In:         "query",
		Help:       helpText(p),
		Required:   p.Required,
		Default:    p.Default,
		Enum:       p.Enum,
		Format:     p.Format,
		Deprecated: p.Deprecated,
	}
	switch p.Type {
	case "integer":
		ps.GoType = "int64"
	case "boolean":
		ps.GoType = "bool"
	case "array":
		ps.GoType = "[]string"
	default:
		ps.GoType = "string"
	}
	return ps
}

func headerParam(p rawir.RawParameter) runtime.ParamSpec {
	return runtime.ParamSpec{
		Name:       p.Name,
		Flag:       camelToKebab(p.Name),
		In:         "header",
		GoType:     "string",
		Help:       helpText(p),
		Required:   p.Required,
		Default:    p.Default,
		Enum:       p.Enum,
		Format:     p.Format,
		Deprecated: p.Deprecated,
	}
}

func formDataParam(p rawir.RawParameter) runtime.ParamSpec {
	ps := runtime.ParamSpec{
		Name:       p.Name,
		Flag:       camelToKebab(p.Name),
		In:         "formData",
		Help:       helpText(p),
		Required:   p.Required,
		Default:    p.Default,
		Enum:       p.Enum,
		Format:     p.Format,
		Deprecated: p.Deprecated,
	}
	switch p.Type {
	case "integer":
		ps.GoType = "int64"
	case "boolean":
		ps.GoType = "bool"
	default:
		ps.GoType = "string"
	}
	return ps
}

func helpText(p rawir.RawParameter) string {
	base := strings.TrimSpace(p.Description)
	if base == "" {
		base = p.Name
	}
	base = firstLine(base)
	var parts []string
	parts = append(parts, p.In)
	if p.Required {
		parts = append(parts, "required")
	}
	if p.Format != "" {
		parts = append(parts, p.Format)
	}
	if len(p.Enum) > 0 {
		parts = append(parts, "one of: "+strings.Join(p.Enum, "|"))
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(parts, ", "))
}

func deriveList(op rawir.RawOperation, defs map[string]*rawir.RawSchema) (string, string) {
	r, ok := op.Responses["200"]
	if !ok || r == nil || r.Schema == nil {
		return "", ""
	}
	s := rawir.Resolve(r.Schema, defs)
	if s == nil {
		return "", ""
	}
	if s.Type == "array" && s.Items != nil {
		return "", s.Items.Ref
	}
	for _, key := range []string{"items", "data", "list"} {
		if v, ok := s.Properties[key]; ok && v != nil {
			vv := rawir.Resolve(v, defs)
			if vv != nil && vv.Type == "array" && vv.Items != nil {
				return key, vv.Items.Ref
			}
		}
	}
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := s.Properties[k]
		if v == nil {
			continue
		}
		vv := rawir.Resolve(v, defs)
		if vv != nil && vv.Type == "array" && vv.Items != nil {
			return k, vv.Items.Ref
		}
	}
	return "", ""
}

const maxDefaultColumns = 6

func defaultColumns(itemRef string, defs map[string]*rawir.RawSchema) []string {
	if itemRef == "" {
		return nil
	}
	if !strings.HasPrefix(itemRef, rawir.RefPrefix) {
		return nil
	}
	item := defs[itemRef[len(rawir.RefPrefix):]]
	if item == nil {
		return nil
	}
	paths := map[string]bool{}
	collectScalarPaths(item, "", 2, paths, defs, map[string]bool{itemRef: true})
	if len(paths) == 0 {
		return nil
	}
	picked := []string{}
	seen := map[string]bool{}
	for _, p := range runtime.PreferredColumns {
		if paths[p] && !seen[p] {
			picked = append(picked, p)
			seen[p] = true
			if len(picked) >= maxDefaultColumns {
				return picked
			}
		}
	}
	if len(picked) >= maxDefaultColumns {
		return picked
	}
	var rest []string
	for p := range paths {
		if seen[p] {
			continue
		}
		if !strings.Contains(p, ".") {
			rest = append(rest, p)
		}
	}
	sort.Strings(rest)
	for _, p := range rest {
		picked = append(picked, p)
		if len(picked) >= maxDefaultColumns {
			break
		}
	}
	return picked
}

func collectScalarPaths(s *rawir.RawSchema, prefix string, maxDepth int, out map[string]bool, defs map[string]*rawir.RawSchema, visited map[string]bool) {
	if s == nil {
		return
	}
	if s.Ref != "" {
		if visited[s.Ref] {
			return
		}
		visited[s.Ref] = true
		s = rawir.Resolve(s, defs)
		if s == nil {
			return
		}
	}
	if s.Properties == nil {
		return
	}
	for k, v := range s.Properties {
		if v == nil {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		vv := v
		if v.Ref != "" {
			if visited[v.Ref] {
				continue
			}
			vv = rawir.Resolve(v, defs)
			if vv == nil {
				continue
			}
		}
		if vv.Type == "array" {
			continue
		}
		if vv.Type == "object" || len(vv.Properties) > 0 {
			if strings.Count(path, ".")+1 < maxDepth {
				next := visited
				if v.Ref != "" {
					next = copyVisited(visited)
					next[v.Ref] = true
				}
				collectScalarPaths(vv, path, maxDepth, out, defs, next)
			}
			continue
		}
		out[path] = true
	}
}

func copyVisited(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func runtimeSchema(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool) *runtime.SchemaSpec {
	if s == nil {
		return nil
	}
	if s.Ref != "" {
		if visited[s.Ref] {
			return &runtime.SchemaSpec{Ref: s.Ref}
		}
		resolved := rawir.Resolve(s, defs)
		if resolved != nil {
			next := copyVisited(visited)
			next[s.Ref] = true
			return runtimeSchema(resolved, defs, next)
		}
	}
	out := &runtime.SchemaSpec{Type: s.Type}
	if s.Ref != "" {
		out.Ref = s.Ref
	}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*runtime.SchemaSpec, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = runtimeSchema(v, defs, visited)
		}
	}
	if s.Items != nil {
		out.Items = runtimeSchema(s.Items, defs, visited)
	}
	return out
}

func deriveResponseMediaType(op rawir.RawOperation) string {
	if r, ok := op.Responses["200"]; ok && r != nil && r.MediaType != "" {
		return r.MediaType
	}
	if len(op.Produces) > 0 {
		return op.Produces[0]
	}
	return ""
}

var paginationTokenParams = map[string]bool{
	"page_token": true, "pageToken": true,
	"cursor": true, "after": true,
	"offset": true, "page": true,
}

var paginationLimitParams = map[string]bool{
	"limit": true, "page_size": true, "pageSize": true,
	"per_page": true, "perPage": true, "maxResults": true,
}

var paginationTokenFields = map[string]bool{
	"next_page_token": true, "nextPageToken": true,
	"next_cursor": true, "nextCursor": true,
	"cursor": true,
}

func derivePagination(op rawir.RawOperation) *runtime.PaginationHint {
	if op.Method != "GET" {
		return nil
	}
	var tokenParam, limitParam string
	for _, p := range op.Parameters {
		if p.In != "query" {
			continue
		}
		if paginationTokenParams[p.Name] {
			tokenParam = p.Name
		}
		if paginationLimitParams[p.Name] {
			limitParam = p.Name
		}
	}
	if tokenParam == "" && limitParam == "" {
		return nil
	}

	strategy := "cursor"
	if tokenParam == "offset" || tokenParam == "page" {
		strategy = "offset"
	}

	var tokenField string
	r, ok := op.Responses["200"]
	if ok && r != nil && r.Schema != nil && r.Schema.Properties != nil {
		for k := range r.Schema.Properties {
			if paginationTokenFields[k] {
				tokenField = k
				break
			}
		}
	}

	return &runtime.PaginationHint{
		Strategy:   strategy,
		TokenParam: tokenParam,
		TokenField: tokenField,
		LimitParam: limitParam,
	}
}

var streamingMediaTypes = map[string]string{
	"text/event-stream":       "sse",
	"application/x-ndjson":    "ndjson",
	"application/stream+json": "ndjson",
}

func deriveStreaming(op rawir.RawOperation) *runtime.StreamingHint {
	for _, ct := range op.Produces {
		if s, ok := streamingMediaTypes[ct]; ok {
			return &runtime.StreamingHint{Strategy: s}
		}
	}
	if r, ok := op.Responses["200"]; ok && r != nil && r.MediaType != "" {
		if s, ok := streamingMediaTypes[r.MediaType]; ok {
			return &runtime.StreamingHint{Strategy: s}
		}
	}
	return nil
}

func deriveSecurity(op rawir.RawOperation) *runtime.SecurityHint {
	if op.Security == nil {
		return nil
	}
	if len(op.Security) == 0 {
		return &runtime.SecurityHint{Public: true}
	}
	seen := map[string]bool{}
	var scopes []string
	for _, req := range op.Security {
		for _, s := range req.Scopes {
			if !seen[s] {
				seen[s] = true
				scopes = append(scopes, s)
			}
		}
	}
	sort.Strings(scopes)
	return &runtime.SecurityHint{Scopes: scopes}
}
