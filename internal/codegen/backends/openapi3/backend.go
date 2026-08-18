package openapi3

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

type oas3Doc struct {
	OpenAPI    string                `json:"openapi" yaml:"openapi"`
	Paths      map[string]*pathItem  `json:"paths" yaml:"paths"`
	Components *components           `json:"components,omitempty" yaml:"components,omitempty"`
	Security   []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
	Servers    []server              `json:"servers,omitempty" yaml:"servers,omitempty"`
}

type server struct {
	URL       string                    `json:"url" yaml:"url"`
	Variables map[string]serverVariable `json:"variables,omitempty" yaml:"variables,omitempty"`
}

type serverVariable struct {
	Default string `json:"default" yaml:"default"`
}

type components struct {
	Schemas map[string]*schemaNode `json:"schemas,omitempty" yaml:"schemas,omitempty"`
}

type pathItem struct {
	Parameters []parameter `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Servers    *[]server   `json:"servers,omitempty" yaml:"servers,omitempty"`
	Get        *operation  `json:"get,omitempty" yaml:"get,omitempty"`
	Post       *operation  `json:"post,omitempty" yaml:"post,omitempty"`
	Put        *operation  `json:"put,omitempty" yaml:"put,omitempty"`
	Delete     *operation  `json:"delete,omitempty" yaml:"delete,omitempty"`
	Patch      *operation  `json:"patch,omitempty" yaml:"patch,omitempty"`
}

type operation struct {
	OperationID string                 `json:"operationId" yaml:"operationId"`
	Tags        []string               `json:"tags" yaml:"tags"`
	Summary     string                 `json:"summary" yaml:"summary"`
	Description string                 `json:"description" yaml:"description"`
	Parameters  []parameter            `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBody *requestBody           `json:"requestBody,omitempty" yaml:"requestBody,omitempty"`
	Responses   map[string]response    `json:"responses" yaml:"responses"`
	Security    *[]map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
	Servers     *[]server              `json:"servers,omitempty" yaml:"servers,omitempty"`

	serverBasePath string
}

type parameter struct {
	Name        string      `json:"name" yaml:"name"`
	In          string      `json:"in" yaml:"in"`
	Required    bool        `json:"required" yaml:"required"`
	Schema      *schemaNode `json:"schema,omitempty" yaml:"schema,omitempty"`
	Description string      `json:"description" yaml:"description"`
	Deprecated  bool        `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
}

type requestBody struct {
	Required bool                 `json:"required" yaml:"required"`
	Content  map[string]mediaType `json:"content" yaml:"content"`
}

type mediaType struct {
	Schema *schemaNode `json:"schema,omitempty" yaml:"schema,omitempty"`
}

type response struct {
	Content map[string]mediaType `json:"content,omitempty" yaml:"content,omitempty"`
}

type schemaNode struct {
	Ref                  string                      `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Type                 schemaType                  `json:"type,omitempty" yaml:"type,omitempty"`
	Format               string                      `json:"format,omitempty" yaml:"format,omitempty"`
	Default              any                         `json:"default,omitempty" yaml:"default,omitempty"`
	Enum                 []any                       `json:"enum,omitempty" yaml:"enum,omitempty"`
	Nullable             bool                        `json:"nullable,omitempty" yaml:"nullable,omitempty"`
	ReadOnly             bool                        `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
	Properties           map[string]*schemaNode      `json:"properties,omitempty" yaml:"properties,omitempty"`
	Required             []string                    `json:"required,omitempty" yaml:"required,omitempty"`
	Items                *schemaNode                 `json:"items,omitempty" yaml:"items,omitempty"`
	AnyOf                []*schemaNode               `json:"anyOf,omitempty" yaml:"anyOf,omitempty"`
	OneOf                []*schemaNode               `json:"oneOf,omitempty" yaml:"oneOf,omitempty"`
	AllOf                []*schemaNode               `json:"allOf,omitempty" yaml:"allOf,omitempty"`
	AdditionalProperties *schemaAdditionalProperties `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
	allowRefSiblings     bool
}

type schemaType struct {
	Value    string
	Nullable bool
}

type schemaAdditionalProperties struct {
	Allowed bool
	Schema  *schemaNode
}

func (t *schemaType) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		t.Value = single
		t.Nullable = false
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("schema type must be string or string array: %w", err)
	}
	primary, err := primarySchemaType(many)
	if err != nil {
		return err
	}
	t.Value = primary
	t.Nullable = primary != "null" && containsType(many, "null")
	return nil
}

func (t *schemaType) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		*t = schemaType{}
		return nil
	}

	var single string
	if err := value.Decode(&single); err == nil {
		t.Value = single
		t.Nullable = false
		return nil
	}

	var many []string
	if err := value.Decode(&many); err != nil {
		return fmt.Errorf("schema type must be string or string array: %w", err)
	}
	primary, err := primarySchemaType(many)
	if err != nil {
		return err
	}
	t.Value = primary
	t.Nullable = primary != "null" && containsType(many, "null")
	return nil
}

func (p *schemaAdditionalProperties) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("true")) || bytes.Equal(data, []byte("false")) {
		p.Schema = nil
		return json.Unmarshal(data, &p.Allowed)
	}
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("additionalProperties must be a boolean or schema")
	}
	var schema schemaNode
	if err := json.Unmarshal(data, &schema); err != nil {
		return err
	}
	p.Allowed = false
	p.Schema = &schema
	return nil
}

func (p *schemaAdditionalProperties) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		return fmt.Errorf("additionalProperties must be a boolean or schema")
	}
	var allowed bool
	if err := value.Decode(&allowed); err == nil {
		p.Allowed = allowed
		p.Schema = nil
		return nil
	}
	var schema schemaNode
	if err := value.Decode(&schema); err != nil {
		return fmt.Errorf("additionalProperties must be a boolean or schema: %w", err)
	}
	p.Allowed = false
	p.Schema = &schema
	return nil
}

func containsType(types []string, want string) bool {
	for _, typ := range types {
		if typ == want {
			return true
		}
	}
	return false
}

func primarySchemaType(types []string) (string, error) {
	if len(types) == 0 {
		return "", fmt.Errorf("schema type array must not be empty")
	}
	primary := ""
	for _, typ := range types {
		if typ == "null" {
			continue
		}
		if primary != "" {
			return "", fmt.Errorf("unsupported schema type union %q", types)
		}
		primary = typ
	}
	if primary == "" {
		return "null", nil
	}
	return primary, nil
}

const oas3RefPrefix = "#/components/schemas/"

func Parse(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	all := &oas3Doc{
		Paths: map[string]*pathItem{},
	}
	allowedOperationIDs := map[string]bool{}
	matchedOperationIDs := map[string]int{}
	if src.OpenAPI3.Expose != nil {
		for _, operationID := range src.OpenAPI3.Expose.OperationIDs {
			allowedOperationIDs[operationID] = true
		}
	}
	for _, rel := range src.OpenAPI3.Files {
		p := filepath.Join(syncDir, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		var doc oas3Doc
		if err := unmarshalAuto(p, data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		markSchemaReferenceSemantics(&doc)
		applyServerBasePaths(&doc, src.Name, p)
		applyEffectiveSecurity(&doc)
		countExposedOperationIDs(&doc, allowedOperationIDs, matchedOperationIDs)
		mergeDoc(all, &doc, src.Name, p)
	}
	mod := toRawIR(src.Name, all)
	if src.OpenAPI3.Expose == nil {
		return mod, nil
	}
	return filterExposedOperations(src.Name, mod, src.OpenAPI3.Expose.OperationIDs, matchedOperationIDs)
}

func markSchemaReferenceSemantics(doc *oas3Doc) {
	allowRefSiblings := schemaReferenceSiblingsAllowed(doc.OpenAPI)
	mark := func(schema *schemaNode) {
		if schema != nil {
			schema.allowRefSiblings = allowRefSiblings
		}
	}
	if doc.Components != nil {
		for _, schema := range doc.Components.Schemas {
			mark(schema)
		}
	}
	for _, item := range doc.Paths {
		for _, parameter := range item.Parameters {
			mark(parameter.Schema)
		}
		for _, op := range []*operation{item.Get, item.Post, item.Put, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			for _, parameter := range op.Parameters {
				mark(parameter.Schema)
			}
			if op.RequestBody != nil {
				for _, mediaType := range op.RequestBody.Content {
					mark(mediaType.Schema)
				}
			}
			for _, response := range op.Responses {
				for _, mediaType := range response.Content {
					mark(mediaType.Schema)
				}
			}
		}
	}
}

func schemaReferenceSiblingsAllowed(version string) bool {
	parts := strings.SplitN(strings.TrimSpace(version), ".", 3)
	if len(parts) < 2 || parts[0] != "3" {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	return err == nil && minor >= 1
}

func countExposedOperationIDs(doc *oas3Doc, allowed map[string]bool, matched map[string]int) {
	if len(allowed) == 0 {
		return
	}
	for _, item := range doc.Paths {
		for _, op := range []*operation{item.Get, item.Post, item.Put, item.Delete, item.Patch} {
			if op != nil && allowed[op.OperationID] {
				matched[op.OperationID]++
			}
		}
	}
}

func filterExposedOperations(source string, mod *rawir.RawModule, operationIDs []string, matched map[string]int) (*rawir.RawModule, error) {
	allowed := make(map[string]bool, len(operationIDs))
	for _, operationID := range operationIDs {
		allowed[operationID] = true
		if matched[operationID] == 0 {
			return nil, fmt.Errorf("openapi3.expose.operation_ids entry %q matched no operations in source %q", operationID, source)
		}
		if matched[operationID] > 1 {
			return nil, fmt.Errorf("openapi3.expose.operation_ids entry %q is ambiguous in source %q: matched %d operations", operationID, source, matched[operationID])
		}
	}

	operations := mod.Operations[:0]
	generated := map[string]bool{}
	for _, op := range mod.Operations {
		if allowed[op.OperationID] {
			operations = append(operations, op)
			generated[op.OperationID] = true
		}
	}
	for _, operationID := range operationIDs {
		if !generated[operationID] {
			return nil, fmt.Errorf("openapi3.expose.operation_ids entry %q was discarded while merging source %q", operationID, source)
		}
	}
	mod.Operations = operations
	return mod, nil
}

func applyServerBasePaths(doc *oas3Doc, module, origin string) {
	rootBasePath := resolveServerBasePath(doc.Servers, module, origin)
	for _, item := range doc.Paths {
		basePath := rootBasePath
		if item.Servers != nil {
			basePath = resolveServerBasePath(*item.Servers, module, origin)
		}
		for _, op := range []*operation{item.Get, item.Post, item.Put, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			op.serverBasePath = basePath
			if op.Servers != nil {
				op.serverBasePath = resolveServerBasePath(*op.Servers, module, origin)
			}
		}
	}
}

func applyEffectiveSecurity(doc *oas3Doc) {
	security := doc.Security
	if security == nil {
		security = []map[string][]string{}
	}
	for _, item := range doc.Paths {
		for _, op := range []*operation{item.Get, item.Post, item.Put, item.Delete, item.Patch} {
			if op != nil && op.Security == nil {
				op.Security = &security
			}
		}
	}
}

func resolveServerBasePath(servers []server, module, origin string) string {
	if len(servers) == 0 {
		return ""
	}
	raw := strings.TrimSpace(servers[0].URL)
	if raw == "" {
		return ""
	}
	for name, variable := range servers[0].Variables {
		raw = strings.ReplaceAll(raw, "{"+name+"}", variable.Default)
	}
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(raw, "{}") || !u.IsAbs() && !strings.HasPrefix(raw, "/") {
		fmt.Fprintf(os.Stderr, "warn: %s: unsupported server URL %q in %s (ignored)\n", module, servers[0].URL, origin)
		return ""
	}
	return strings.TrimRight(u.EscapedPath(), "/")
}

func unmarshalAuto(path string, data []byte, v any) error {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, v)
	default:
		return json.Unmarshal(data, v)
	}
}

func mergeDoc(dst, add *oas3Doc, module, origin string) {
	if add.Components != nil && len(add.Components.Schemas) > 0 {
		if dst.Components == nil {
			dst.Components = &components{Schemas: map[string]*schemaNode{}}
		}
		for k, v := range add.Components.Schemas {
			if existing, exists := dst.Components.Schemas[k]; exists {
				if !sameJSON(existing, v) {
					fmt.Fprintf(os.Stderr, "warn: %s: diverging schema %q in %s (kept first)\n", module, k, origin)
				}
				continue
			}
			dst.Components.Schemas[k] = v
		}
	}
	for path, item := range add.Paths {
		if _, exists := dst.Paths[path]; !exists {
			dst.Paths[path] = item
			continue
		}
		existing := dst.Paths[path]
		if item.Get != nil && existing.Get == nil {
			existing.Get = item.Get
		}
		if item.Post != nil && existing.Post == nil {
			existing.Post = item.Post
		}
		if item.Put != nil && existing.Put == nil {
			existing.Put = item.Put
		}
		if item.Delete != nil && existing.Delete == nil {
			existing.Delete = item.Delete
		}
		if item.Patch != nil && existing.Patch == nil {
			existing.Patch = item.Patch
		}
	}
}

func sameJSON(a, b any) bool {
	ja, err := json.Marshal(a)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ja) == string(jb)
}

func toRawIR(name string, doc *oas3Doc) *rawir.RawModule {
	mod := &rawir.RawModule{
		Name:    name,
		Schemas: map[string]*rawir.RawSchema{},
	}
	if doc.Components != nil {
		for k, v := range doc.Components.Schemas {
			mod.Schemas[k] = convertSchema(v)
		}
	}
	for path, item := range doc.Paths {
		pathParams := item.Parameters
		for _, pair := range []struct {
			method string
			op     *operation
		}{
			{"GET", item.Get},
			{"POST", item.Post},
			{"PUT", item.Put},
			{"DELETE", item.Delete},
			{"PATCH", item.Patch},
		} {
			if pair.op == nil {
				continue
			}
			mod.Operations = append(mod.Operations, convertOp(pair.op, pair.method, path, pathParams, doc.Security))
		}
	}
	return mod
}

func convertOp(op *operation, method, path string, pathParams []parameter, globalSecurity []map[string][]string) rawir.RawOperation {
	out := rawir.RawOperation{
		OperationID:    op.OperationID,
		Summary:        op.Summary,
		Description:    op.Description,
		Method:         method,
		Path:           path,
		ServerBasePath: op.serverBasePath,
		Responses:      map[string]*rawir.RawResponse{},
	}
	if len(op.Tags) > 0 && op.Tags[0] != "" {
		out.Group = op.Tags[0]
	}

	seen := map[string]bool{}
	for _, p := range op.Parameters {
		seen[p.Name] = true
		out.Parameters = append(out.Parameters, convertParam(p))
	}
	for _, p := range pathParams {
		if seen[p.Name] {
			continue
		}
		out.Parameters = append(out.Parameters, convertParam(p))
	}

	if op.RequestBody != nil {
		out.RequestBody = &rawir.RawRequestBody{Required: op.RequestBody.Required}
		if mt, ok := op.RequestBody.Content["application/json"]; ok {
			out.RequestBody.MediaType = "application/json"
			out.RequestBody.Schema = convertSchema(mt.Schema)
		} else if len(op.RequestBody.Content) > 0 {
			mediaTypes := make([]string, 0, len(op.RequestBody.Content))
			for ct := range op.RequestBody.Content {
				mediaTypes = append(mediaTypes, ct)
			}
			sort.Strings(mediaTypes)
			out.RequestBody.MediaType = mediaTypes[0]
			out.RequestBody.Schema = convertSchema(op.RequestBody.Content[mediaTypes[0]].Schema)
		}
	}

	for code, resp := range op.Responses {
		rs := &rawir.RawResponse{}
		if mt, ok := resp.Content["application/json"]; ok {
			rs.Schema = convertSchema(mt.Schema)
			rs.MediaType = "application/json"
		} else {
			mediaTypes := make([]string, 0, len(resp.Content))
			for mediaType := range resp.Content {
				if mediaType != "" {
					mediaTypes = append(mediaTypes, mediaType)
				}
			}
			sort.Strings(mediaTypes)
			if len(mediaTypes) > 0 {
				mediaType := mediaTypes[0]
				rs.Schema = convertSchema(resp.Content[mediaType].Schema)
				rs.MediaType = mediaType
			}
		}
		out.Responses[code] = rs
	}
	if r, ok := out.Responses["200"]; ok && r.MediaType != "" {
		out.Produces = []string{r.MediaType}
	}
	sec := globalSecurity
	if op.Security != nil {
		sec = *op.Security
	}
	out.Security = convertSecurity(sec)
	return out
}

func convertSecurity(sec []map[string][]string) []rawir.RawSecurityReq {
	if sec == nil {
		return nil
	}
	var out []rawir.RawSecurityReq
	for _, req := range sec {
		var scopes []string
		for _, s := range req {
			scopes = append(scopes, s...)
		}
		out = append(out, rawir.RawSecurityReq{Scopes: scopes})
	}
	if out == nil {
		out = []rawir.RawSecurityReq{}
	}
	return out
}

func convertParam(p parameter) rawir.RawParameter {
	typ := "string"
	var format, def string
	var enum []string
	if p.Schema != nil {
		if p.Schema.Type.Value != "" {
			typ = p.Schema.Type.Value
		}
		format = p.Schema.Format
		def = anyToString(p.Schema.Default)
		enum = anySliceToStrings(p.Schema.Enum)
	}
	return rawir.RawParameter{
		Name:        p.Name,
		In:          p.In,
		Required:    p.Required,
		Type:        typ,
		Description: p.Description,
		Default:     def,
		Enum:        enum,
		Format:      format,
		Deprecated:  p.Deprecated,
	}
}

func anyToString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func anySliceToStrings(vs []any) []string {
	if len(vs) == 0 {
		return nil
	}
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = fmt.Sprintf("%v", v)
	}
	return out
}

func convertSchema(s *schemaNode) *rawir.RawSchema {
	if s == nil {
		return nil
	}
	return convertSchemaWithReferenceSemantics(s, s.allowRefSiblings)
}

func convertSchemaWithReferenceSemantics(s *schemaNode, allowRefSiblings bool) *rawir.RawSchema {
	if s == nil {
		return nil
	}
	if s.Ref != "" && !allowRefSiblings {
		return &rawir.RawSchema{Ref: normalizedSchemaRef(s.Ref)}
	}
	if len(s.OneOf) == 0 && len(s.AllOf) == 0 {
		if option, ok := nullableAlternative(s, s.AnyOf); ok {
			out := convertSchemaWithReferenceSemantics(option, allowRefSiblings)
			out.Nullable = true
			out.ReadOnly = out.ReadOnly || s.ReadOnly
			return out
		}
	}
	out := &rawir.RawSchema{
		Type:     s.Type.Value,
		Format:   s.Format,
		Nullable: s.Type.Nullable || (!allowRefSiblings && s.Nullable && s.Type.Value != ""),
		ReadOnly: s.ReadOnly,
	}
	if s.Ref != "" {
		out.Ref = normalizedSchemaRef(s.Ref)
	}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*rawir.RawSchema, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = convertSchemaWithReferenceSemantics(v, allowRefSiblings)
		}
	}
	if len(s.Required) > 0 {
		out.Required = append([]string(nil), s.Required...)
	}
	if s.Items != nil {
		out.Items = convertSchemaWithReferenceSemantics(s.Items, allowRefSiblings)
	}
	out.AnyOf = convertSchemas(s.AnyOf, allowRefSiblings)
	out.OneOf = convertSchemas(s.OneOf, allowRefSiblings)
	out.AllOf = convertSchemas(s.AllOf, allowRefSiblings)
	if s.AdditionalProperties != nil {
		out.AdditionalProperties = &rawir.RawAdditionalProperties{
			Allowed: s.AdditionalProperties.Allowed,
			Schema:  convertSchemaWithReferenceSemantics(s.AdditionalProperties.Schema, allowRefSiblings),
		}
	}
	return out
}

func normalizedSchemaRef(ref string) string {
	if strings.HasPrefix(ref, oas3RefPrefix) {
		return rawir.RefPrefix + ref[len(oas3RefPrefix):]
	}
	return ref
}

func nullableAlternative(schema *schemaNode, options []*schemaNode) (*schemaNode, bool) {
	if len(options) != 2 || schema.Ref != "" || schema.Type.Value != "" || schema.Format != "" || len(schema.Properties) > 0 || len(schema.Required) > 0 || schema.Items != nil || schema.AdditionalProperties != nil {
		return nil, false
	}
	if isNullSchema(options[0]) {
		return nullableAlternativeCandidate(options[1])
	}
	if isNullSchema(options[1]) {
		return nullableAlternativeCandidate(options[0])
	}
	return nil, false
}

func nullableAlternativeCandidate(schema *schemaNode) (*schemaNode, bool) {
	if schema == nil || isNullSchema(schema) || len(schema.AnyOf) > 0 || len(schema.OneOf) > 0 || len(schema.AllOf) > 0 {
		return nil, false
	}
	if schema.Ref != "" && schemaNodeHasReferenceSiblings(schema) {
		return nil, false
	}
	return schema, true
}

func schemaNodeHasReferenceSiblings(schema *schemaNode) bool {
	return schema.Type.Value != "" || schema.Format != "" || schema.Nullable || schema.ReadOnly || len(schema.Properties) > 0 || len(schema.Required) > 0 || schema.Items != nil || schema.AdditionalProperties != nil
}

func isNullSchema(schema *schemaNode) bool {
	return schema != nil && schema.Type.Value == "null"
}

func convertSchemas(schemas []*schemaNode, allowRefSiblings bool) []*rawir.RawSchema {
	if len(schemas) == 0 {
		return nil
	}
	out := make([]*rawir.RawSchema, len(schemas))
	for i, schema := range schemas {
		out[i] = convertSchemaWithReferenceSemantics(schema, allowRefSiblings)
	}
	return out
}
