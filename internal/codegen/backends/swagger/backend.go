package swagger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"gopkg.in/yaml.v3"
)

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

type swaggerDoc struct {
	Produces    []string                        `json:"produces"`
	Consumes    []string                        `json:"consumes"`
	Definitions map[string]*schemaNode          `json:"definitions"`
	Paths       map[string]map[string]operation `json:"paths"`
	Security    []map[string][]string           `json:"security"`
}

type operation struct {
	OperationID string                 `json:"operationId"`
	Tags        []string               `json:"tags"`
	Parameters  []parameter            `json:"parameters"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Responses   map[string]response    `json:"responses"`
	Produces    []string               `json:"produces"`
	Consumes    []string               `json:"consumes"`
	Security    *[]map[string][]string `json:"security"`
}

type parameter struct {
	Name        string      `json:"name"`
	In          string      `json:"in"`
	Required    bool        `json:"required"`
	Type        string      `json:"type"`
	Format      string      `json:"format,omitempty"`
	Description string      `json:"description"`
	Schema      *schemaNode `json:"schema,omitempty"`
	Default     any         `json:"default,omitempty"`
	Enum        []any       `json:"enum,omitempty"`
	Deprecated  bool        `json:"x-deprecated,omitempty"`
}

type response struct {
	Schema *schemaNode `json:"schema,omitempty"`
}

type schemaNode struct {
	Ref        string                 `json:"$ref,omitempty"`
	Type       string                 `json:"type,omitempty"`
	ReadOnly   bool                   `json:"readOnly,omitempty"`
	Properties map[string]*schemaNode `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
	Items      *schemaNode            `json:"items,omitempty"`
}

func Parse(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	all := &swaggerDoc{
		Definitions: map[string]*schemaNode{},
		Paths:       map[string]map[string]operation{},
	}
	for _, rel := range src.Swagger.Files {
		p := filepath.Join(syncDir, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".yaml" || ext == ".yml" {
			var document any
			if err := yaml.Unmarshal(data, &document); err != nil {
				return nil, fmt.Errorf("parse %s: %w", p, err)
			}
			data, err = json.Marshal(document)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", p, err)
			}
		}
		var sw swaggerDoc
		if err := json.Unmarshal(data, &sw); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		applyEffectiveSecurity(&sw)
		applyEffectiveConsumes(&sw)
		mergeDoc(all, &sw, src.Name, p)
	}
	return toRawIR(src.Name, all), nil
}

func applyEffectiveConsumes(doc *swaggerDoc) {
	for _, methods := range doc.Paths {
		for method, op := range methods {
			if len(op.Consumes) == 0 {
				op.Consumes = append([]string(nil), doc.Consumes...)
				methods[method] = op
			}
		}
	}
}

func applyEffectiveSecurity(doc *swaggerDoc) {
	security := doc.Security
	if security == nil {
		security = []map[string][]string{}
	}
	for _, methods := range doc.Paths {
		for method, op := range methods {
			if op.Security == nil {
				op.Security = &security
				methods[method] = op
			}
		}
	}
}

func mergeDoc(dst, add *swaggerDoc, module, origin string) {
	for k, v := range add.Definitions {
		if existing, exists := dst.Definitions[k]; exists {
			if !sameJSON(existing, v) {
				fmt.Fprintf(os.Stderr, "warn: %s: diverging definition %q in %s (kept first)\n", module, k, origin)
			}
			continue
		}
		dst.Definitions[k] = v
	}
	for path, methods := range add.Paths {
		bucket, ok := dst.Paths[path]
		if !ok {
			bucket = map[string]operation{}
			dst.Paths[path] = bucket
		}
		for m, op := range methods {
			if _, exists := bucket[m]; exists {
				fmt.Fprintf(os.Stderr, "warn: %s: duplicate %s %s in %s (kept first)\n", module, strings.ToUpper(m), path, origin)
				continue
			}
			bucket[m] = op
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

func toRawIR(name string, doc *swaggerDoc) *rawir.RawModule {
	mod := &rawir.RawModule{
		Name:    name,
		Schemas: make(map[string]*rawir.RawSchema, len(doc.Definitions)),
	}
	for k, v := range doc.Definitions {
		mod.Schemas[k] = convertSchema(v)
	}
	for path, methods := range doc.Paths {
		for _, m := range []string{"get", "post", "put", "delete", "patch"} {
			op, ok := methods[m]
			if !ok {
				continue
			}
			mod.Operations = append(mod.Operations, convertOp(op, m, path, doc.Produces, doc.Security))
		}
	}
	return mod
}

func convertOp(op operation, method, path string, docProduces []string, globalSecurity []map[string][]string) rawir.RawOperation {
	out := rawir.RawOperation{
		OperationID: op.OperationID,
		Summary:     op.Summary,
		Description: op.Description,
		Method:      strings.ToUpper(method),
		Path:        path,
		Responses:   map[string]*rawir.RawResponse{},
	}
	if len(op.Tags) > 0 && op.Tags[0] != "" {
		out.Group = op.Tags[0]
	}
	produces := op.Produces
	if len(produces) == 0 {
		produces = docProduces
	}
	out.Produces = produces
	seenParameters := map[string]parameter{}
	for _, p := range op.Parameters {
		key := p.In + "\x00" + p.Name
		if existing, ok := seenParameters[key]; ok {
			if !sameJSON(existing, p) {
				fmt.Fprintf(os.Stderr, "warn: diverging duplicate parameter %q in %s %s (kept first)\n", p.Name, out.Method, path)
			}
			continue
		}
		seenParameters[key] = p
		if p.In == "body" {
			out.RequestBody = &rawir.RawRequestBody{Required: p.Required, MediaType: preferredRequestMediaType(op.Consumes), Schema: convertSchema(p.Schema)}
			continue
		}
		out.Parameters = append(out.Parameters, rawir.RawParameter{
			Name:        p.Name,
			In:          p.In,
			Required:    p.Required,
			Type:        p.Type,
			Description: p.Description,
			Default:     anyToString(p.Default),
			Enum:        anySliceToStrings(p.Enum),
			Format:      p.Format,
			Deprecated:  p.Deprecated,
		})
	}
	for code, resp := range op.Responses {
		out.Responses[code] = &rawir.RawResponse{Schema: convertSchema(resp.Schema)}
	}
	sec := globalSecurity
	if op.Security != nil {
		sec = *op.Security
	}
	out.Security = convertSecurity(sec)
	return out
}

func preferredRequestMediaType(values []string) string {
	for _, value := range values {
		mediaType, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ";")
		mediaType = strings.TrimSpace(mediaType)
		if mediaType == "application/json" {
			return value
		}
	}
	for _, value := range values {
		mediaType, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ";")
		if strings.HasSuffix(strings.TrimSpace(mediaType), "+json") {
			return value
		}
	}
	if len(values) == 0 {
		return ""
	}
	return values[0]
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

func convertSchema(s *schemaNode) *rawir.RawSchema {
	if s == nil {
		return nil
	}
	out := &rawir.RawSchema{
		Ref:      s.Ref,
		Type:     s.Type,
		ReadOnly: s.ReadOnly,
	}
	if len(s.Properties) > 0 {
		out.Properties = make(map[string]*rawir.RawSchema, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = convertSchema(v)
		}
	}
	if len(s.Required) > 0 {
		out.Required = append([]string(nil), s.Required...)
	}
	if s.Items != nil {
		out.Items = convertSchema(s.Items)
	}
	return out
}
