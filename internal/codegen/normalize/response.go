package normalize

import (
	"maps"
	"mime"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func applyRawOutputHints(spec *runtime.CommandSpec, hints *rawir.RawOutputHints) {
	if hints == nil {
		return
	}
	if hints.ListPath != "" {
		spec.Output.ListPath = hints.ListPath
	}
	if len(hints.DefaultColumns) > 0 {
		spec.Output.DefaultColumns = append([]string(nil), hints.DefaultColumns...)
	}
	if hints.ResponseMediaType != "" {
		spec.Output.ResponseMediaType = hints.ResponseMediaType
	}
	if hints.Pagination != nil {
		spec.Output.Pagination = &runtime.PaginationHint{
			Strategy:   hints.Pagination.Strategy,
			TokenParam: hints.Pagination.TokenParam,
			TokenField: hints.Pagination.TokenField,
			LimitParam: hints.Pagination.LimitParam,
		}
	}
}

func deriveList(op rawir.RawOperation, defs map[string]*rawir.RawSchema) (string, string) {
	schema := compatibleResponseSchema(op, defs)
	if schema == nil {
		return "", ""
	}
	s := rawir.Resolve(schema, defs)
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
	keys := slices.Sorted(maps.Keys(s.Properties))
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
					next = maps.Clone(visited)
					next[v.Ref] = true
				}
				collectScalarPaths(vv, path, maxDepth, out, defs, next)
			}
			continue
		}
		out[path] = true
	}
}

func deriveResponseMediaType(op rawir.RawOperation) string {
	responses := successResponses(op)
	if len(responses) == 0 {
		return ""
	}
	if r, ok := op.Responses["200"]; ok {
		if r != nil && r.MediaType != "" {
			return r.MediaType
		}
		if len(op.Produces) > 0 {
			return op.Produces[0]
		}
		return ""
	}

	mediaType := ""
	for _, response := range responses {
		mediaTypes := responseMediaTypes(op, response)
		if len(mediaTypes) != 1 {
			return ""
		}
		if mediaType == "" {
			mediaType = mediaTypes[0]
			continue
		}
		if mediaType != mediaTypes[0] {
			return ""
		}
	}
	return mediaType
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

func derivePagination(op rawir.RawOperation, defs map[string]*rawir.RawSchema) *runtime.PaginationHint {
	if op.Method != "GET" {
		return nil
	}
	schema := compatibleResponseSchema(op, defs)
	if schema == nil {
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
	if schema = rawir.Resolve(schema, defs); schema != nil {
		keys := slices.Sorted(maps.Keys(schema.Properties))
		for _, k := range keys {
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
	responses := successResponses(op)
	if len(responses) == 0 {
		return nil
	}
	strategy := ""
	for _, response := range responses {
		mediaTypes := responseMediaTypes(op, response)
		if len(mediaTypes) == 0 {
			return nil
		}
		for _, mediaType := range mediaTypes {
			current := streamingStrategy(mediaType)
			if current == "" {
				return nil
			}
			if strategy == "" {
				strategy = current
				continue
			}
			if strategy != current {
				return nil
			}
		}
	}
	return &runtime.StreamingHint{Strategy: strategy}
}

func successResponses(op rawir.RawOperation) []*rawir.RawResponse {
	if response, ok := op.Responses["200"]; ok {
		return []*rawir.RawResponse{response}
	}
	codes := make([]string, 0, len(op.Responses))
	for code := range op.Responses {
		if len(code) != 3 {
			continue
		}
		status, err := strconv.Atoi(code)
		if err == nil && status >= 200 && status <= 299 {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	responses := make([]*rawir.RawResponse, 0, len(codes))
	for _, code := range codes {
		responses = append(responses, op.Responses[code])
	}
	return responses
}

func responseMediaTypes(op rawir.RawOperation, response *rawir.RawResponse) []string {
	if response != nil && response.MediaType != "" {
		return []string{response.MediaType}
	}
	seen := map[string]bool{}
	var mediaTypes []string
	for _, mediaType := range op.Produces {
		if mediaType != "" && !seen[mediaType] {
			seen[mediaType] = true
			mediaTypes = append(mediaTypes, mediaType)
		}
	}
	return mediaTypes
}

func compatibleResponseSchema(op rawir.RawOperation, defs map[string]*rawir.RawSchema) *rawir.RawSchema {
	responses := successResponses(op)
	if len(responses) == 0 {
		return nil
	}
	var schema *rawir.RawSchema
	var normalized *runtime.SchemaSpec
	for _, response := range responses {
		if response == nil || response.Schema == nil {
			return nil
		}
		for _, mediaType := range responseMediaTypes(op, response) {
			if !isJSONMediaType(mediaType) || streamingStrategy(mediaType) != "" {
				return nil
			}
		}
		current := runtimeSchema(response.Schema, defs, map[string]bool{})
		if schema == nil {
			schema = response.Schema
			normalized = current
			continue
		}
		if !reflect.DeepEqual(normalized, current) {
			return nil
		}
	}
	return schema
}

func isJSONMediaType(mediaType string) bool {
	mediaType = baseMediaType(mediaType)
	return mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")
}

func streamingStrategy(mediaType string) string {
	return streamingMediaTypes[baseMediaType(mediaType)]
}

func baseMediaType(mediaType string) string {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return ""
	}
	return strings.ToLower(base)
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
