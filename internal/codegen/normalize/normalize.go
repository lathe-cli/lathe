package normalize

import (
	"fmt"
	"mime"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func Normalize(mod *rawir.RawModule) []runtime.CommandSpec {
	trim := commonNoisePrefix(mod.Operations)
	var specs []runtime.CommandSpec
	for _, op := range mod.Operations {
		// Fall back to a method+path-derived id when the spec omits operationId
		// (common with swaggo/springfox and framework-extracted drafts); without
		// this the operation is silently dropped and the CLI is empty.
		opID := op.OperationID
		useName := ""
		if opID == "" {
			opID = synthOperationID(op.Method, op.Path)
			useName = synthUseName(op.Method, op.Path, trim)
		} else {
			useName = kebabFromID(opNameFromID(opID, group(op), mod.Name))
		}
		if opID == "" || useName == "" {
			continue
		}
		spec := runtime.CommandSpec{
			Group:       group(op),
			Use:         useName,
			Short:       pickShort(op),
			OperationID: opID,
			Method:      op.Method,
			PathTpl:     joinBasePath(op.ServerBasePath, op.Path),
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
			case "variable":
				spec.Params = append(spec.Params, variableParam(pp))
			}
		}
		if op.RequestBody != nil {
			spec.RequestBody = &runtime.RequestBody{
				Required:  op.RequestBody.Required,
				MediaType: op.RequestBody.MediaType,
				Schema:    runtimeRequestSchema(op.RequestBody.Schema, mod.Schemas),
				Template:  op.RequestBody.Template,
				MergePath: op.RequestBody.MergePath,
			}
			bodyParams := multipartBodyParams(op.RequestBody, mod.Schemas)
			disambiguateMultipartParamFlags(spec.Params, bodyParams)
			spec.Params = append(spec.Params, bodyParams...)
		}
		normalizeParamFlags(spec.Params)
		lp, itemRef := deriveList(op, mod.Schemas)
		spec.Output.ListPath = lp
		if itemRef != "" {
			spec.Output.DefaultColumns = defaultColumns(itemRef, mod.Schemas)
		}
		spec.Output.ResponseMediaType = deriveResponseMediaType(op)
		spec.Output.Pagination = derivePagination(op, mod.Schemas)
		spec.Output.Streaming = deriveStreaming(op)
		applyRawOutputHints(&spec, op.Output)
		spec.Security = deriveSecurity(op)
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Group != specs[j].Group {
			return specs[i].Group < specs[j].Group
		}
		if specs[i].Use != specs[j].Use {
			return specs[i].Use < specs[j].Use
		}
		if specs[i].OperationID != specs[j].OperationID {
			return specs[i].OperationID < specs[j].OperationID
		}
		// Group+Use+OperationID is not a total order: a spec can reuse one
		// operationId across API versions, e.g. the same Foo_Get on /v1alpha1
		// and /v1alpha2. sort.Slice is not stable, so without a tie-break on
		// the HTTP identity those specs come out in a different order on every
		// run, and any later step keyed on position becomes nondeterministic.
		if specs[i].PathTpl != specs[j].PathTpl {
			return specs[i].PathTpl < specs[j].PathTpl
		}
		return specs[i].Method < specs[j].Method
	})
	return specs
}

func joinBasePath(basePath, operationPath string) string {
	basePath = strings.TrimRight(basePath, "/")
	if basePath == "" {
		return operationPath
	}
	return "/" + strings.TrimLeft(basePath, "/") + "/" + strings.TrimLeft(operationPath, "/")
}

// synthOperationID builds a camelCase id like "getUsersId" from a method and
// path, for specs that omit operationId. It stands in for the missing id in the
// catalog and Skill; the command name comes from synthUseName instead.
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

// synthUseName names an operation the spec never named, from the path rather
// than the id: "delete-dashboard-pk-favorites", not
// "delete-api-v1dashboard-pk-favorites".
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

// commonNoisePrefix counts leading segments ("api", "v1") shared by every
// unnamed operation. Module-wide rather than per-operation, so a spec serving
// two API versions keeps them distinct instead of collapsing onto one name.
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
		// Always leave one segment: a command needs a noun.
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

func group(op rawir.RawOperation) string {
	if op.Group != "" {
		return op.Group
	}
	return "Default"
}

// Blind prefix stripping collapses create_chunk/update_chunk/delete_chunk onto one name.
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

// kebabFromID renders an operationId as a command name. Edge separators are
// trimmed: "_foo" would otherwise yield "-foo", which cobra reads as a flag.
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

func variableParam(p rawir.RawParameter) runtime.ParamSpec {
	ps := runtime.ParamSpec{
		Name:       p.Name,
		Flag:       variableFlagName(p.Name),
		In:         runtime.InVariable,
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
	case "number":
		ps.GoType = "float64"
	case "boolean":
		ps.GoType = "bool"
	case "integer-array":
		ps.GoType = "[]int64"
	case "number-array":
		ps.GoType = "[]float64"
	case "boolean-array":
		ps.GoType = "[]bool"
	case "string-array", "array":
		ps.GoType = "[]string"
	default:
		ps.GoType = "string"
	}
	return ps
}

func variableFlagName(name string) string {
	return strings.ReplaceAll(camelToKebab(name), ".", "-")
}

func normalizeParamFlags(params []runtime.ParamSpec) {
	desired := make([]string, len(params))
	desiredCount := make(map[string]int, len(params))
	for i, param := range params {
		desired[i] = strings.Trim(collapseDashes(strings.ReplaceAll(param.Flag, "_", "-")), "-")
		desiredCount[desired[i]]++
	}
	for i := range params {
		if desired[i] == "" || desired[i] == params[i].Flag || desiredCount[desired[i]] > 1 {
			continue
		}
		params[i].Aliases = []string{params[i].Flag}
		params[i].Flag = desired[i]
	}
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
	format := p.Format
	if p.Type == "file" && format == "" {
		format = "binary"
	}
	p.Format = format
	ps := runtime.ParamSpec{
		Name:       p.Name,
		Flag:       camelToKebab(p.Name),
		In:         "formData",
		Help:       helpText(p),
		Required:   p.Required,
		Default:    p.Default,
		Enum:       p.Enum,
		Format:     format,
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

func multipartBodyParams(body *rawir.RawRequestBody, defs map[string]*rawir.RawSchema) []runtime.ParamSpec {
	mediaType, _, err := mime.ParseMediaType(body.MediaType)
	if err != nil || mediaType != "multipart/form-data" {
		return nil
	}
	schema := rawir.Resolve(body.Schema, defs)
	if schema == nil || schema.Type != "object" || len(schema.Properties) == 0 {
		return nil
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]runtime.ParamSpec, 0, len(names))
	for _, name := range names {
		property := rawir.Resolve(schema.Properties[name], defs)
		if property == nil || !multipartScalar(property) {
			return nil
		}
		out = append(out, formDataParam(rawir.RawParameter{
			Name:     name,
			In:       "formData",
			Required: required[name],
			Type:     property.Type,
			Format:   property.Format,
		}))
	}
	return out
}

func multipartScalar(schema *rawir.RawSchema) bool {
	if schema.Format == "binary" {
		return schema.Type == "" || schema.Type == "string"
	}
	switch schema.Type {
	case "string", "integer", "boolean":
		return true
	default:
		return false
	}
}

func disambiguateMultipartParamFlags(existing, body []runtime.ParamSpec) {
	used := make(map[string]bool, len(existing)+len(body))
	for _, param := range existing {
		used[param.Flag] = true
	}
	for i := range body {
		flag := body[i].Flag
		if used[flag] {
			base := "body-" + flag
			flag = base
			for suffix := 2; used[flag]; suffix++ {
				flag = fmt.Sprintf("%s-%d", base, suffix)
			}
			body[i].Flag = flag
		}
		used[flag] = true
	}
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
	if p.In == "formData" && p.Format == "binary" {
		parts = append(parts, "local file path")
	}
	if len(p.Enum) > 0 {
		parts = append(parts, "one of: "+strings.Join(p.Enum, "|"))
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(parts, ", "))
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
	return runtimeSchemaForUse(s, defs, visited, false)
}

func runtimeRequestSchema(s *rawir.RawSchema, defs map[string]*rawir.RawSchema) *runtime.SchemaSpec {
	return runtimeSchemaForUse(s, defs, map[string]bool{}, true)
}

func runtimeSchemaForUse(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool, request bool) *runtime.SchemaSpec {
	if s == nil {
		return nil
	}
	if s.Ref != "" && !visited[s.Ref] {
		if resolved := rawir.Resolve(s, defs); resolved != nil {
			next := copyVisited(visited)
			next[s.Ref] = true
			base := runtimeSchemaForUse(resolved, defs, next, request)
			if rawSchemaHasRefSiblings(s) {
				sibling := *s
				sibling.Ref = ""
				return &runtime.SchemaSpec{AllOf: []*runtime.SchemaSpec{base, runtimeSchemaForUse(&sibling, defs, visited, request)}}
			}
			base.Nullable = base.Nullable || s.Nullable
			return base
		}
	}
	out := &runtime.SchemaSpec{
		Ref:                        s.Ref,
		Type:                       s.Type,
		Nullable:                   s.Nullable,
		AcceptStringEncodedInteger: s.AcceptStringEncodedInteger,
		AcceptStringEncodedNumber:  s.AcceptStringEncodedNumber,
		AcceptIntegerEnum:          s.AcceptIntegerEnum,
	}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*runtime.SchemaSpec, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = runtimeSchemaForUse(v, defs, visited, request)
		}
	}
	if len(s.Required) > 0 {
		for _, name := range s.Required {
			if request && rawSchemaPropertyIsReadOnly(s, name, defs, map[string]bool{}) {
				continue
			}
			out.Required = append(out.Required, name)
		}
	}
	if s.Items != nil {
		out.Items = runtimeSchemaForUse(s.Items, defs, visited, request)
	}
	if len(s.AnyOf) > 0 {
		out.AnyOf = runtimeSchemasForUse(s.AnyOf, defs, visited, request)
	}
	if len(s.OneOf) > 0 {
		out.OneOf = runtimeSchemasForUse(s.OneOf, defs, visited, request)
	}
	if len(s.AllOf) > 0 {
		out.AllOf = runtimeSchemasForUse(s.AllOf, defs, visited, request)
	}
	if s.AdditionalProperties != nil {
		out.AdditionalProperties = &runtime.AdditionalPropertiesSpec{
			Allowed: s.AdditionalProperties.Allowed,
			Schema:  runtimeSchemaForUse(s.AdditionalProperties.Schema, defs, visited, request),
		}
	}
	return out
}

func rawSchemaHasRefSiblings(s *rawir.RawSchema) bool {
	return s.Type != "" || s.AcceptStringEncodedInteger || s.AcceptStringEncodedNumber || s.AcceptIntegerEnum || len(s.Properties) > 0 || len(s.Required) > 0 || s.Items != nil || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || len(s.AllOf) > 0 || s.AdditionalProperties != nil
}

func rawSchemaIsReadOnly(s *rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool) bool {
	if s == nil {
		return false
	}
	if s.ReadOnly {
		return true
	}
	if s.Ref != "" && !visited[s.Ref] {
		next := copyVisited(visited)
		next[s.Ref] = true
		if rawSchemaIsReadOnly(rawir.Resolve(s, defs), defs, next) {
			return true
		}
	}
	for _, schema := range s.AllOf {
		if rawSchemaIsReadOnly(schema, defs, copyVisited(visited)) {
			return true
		}
	}
	return false
}

func rawSchemaPropertyIsReadOnly(s *rawir.RawSchema, name string, defs map[string]*rawir.RawSchema, visited map[string]bool) bool {
	if s == nil {
		return false
	}
	if rawSchemaIsReadOnly(s.Properties[name], defs, copyVisited(visited)) {
		return true
	}
	if s.Ref != "" && !visited[s.Ref] {
		next := copyVisited(visited)
		next[s.Ref] = true
		if rawSchemaPropertyIsReadOnly(rawir.Resolve(s, defs), name, defs, next) {
			return true
		}
	}
	for _, schema := range s.AllOf {
		if rawSchemaPropertyIsReadOnly(schema, name, defs, copyVisited(visited)) {
			return true
		}
	}
	return false
}

func runtimeSchemasForUse(schemas []*rawir.RawSchema, defs map[string]*rawir.RawSchema, visited map[string]bool, request bool) []*runtime.SchemaSpec {
	if len(schemas) == 0 {
		return nil
	}
	out := make([]*runtime.SchemaSpec, len(schemas))
	for i, schema := range schemas {
		out[i] = runtimeSchemaForUse(schema, defs, visited, request)
	}
	return out
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
		keys := make([]string, 0, len(schema.Properties))
		for k := range schema.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
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
