package swagger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/backends/document"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"gopkg.in/yaml.v3"
)

type swaggerDoc struct {
	Tags                []document.Tag                     `json:"tags"`
	Produces            []string                           `json:"produces"`
	Definitions         map[string]*schemaNode             `json:"definitions"`
	SecurityDefinitions map[string]document.SecurityScheme `json:"securityDefinitions"`
	Paths               map[string]map[string]operation    `json:"paths"`
	Security            []map[string][]string              `json:"security"`
}

type operation struct {
	OperationID string                 `json:"operationId"`
	Tags        []string               `json:"tags"`
	Parameters  []parameter            `json:"parameters"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Responses   map[string]response    `json:"responses"`
	Produces    []string               `json:"produces"`
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
	Ref                  string                      `json:"$ref,omitempty"`
	Type                 string                      `json:"type,omitempty"`
	Description          string                      `json:"description,omitempty"`
	Format               string                      `json:"format,omitempty"`
	Enum                 []any                       `json:"enum,omitempty"`
	Nullable             bool                        `json:"x-nullable,omitempty"`
	ReadOnly             bool                        `json:"readOnly,omitempty"`
	Properties           map[string]*schemaNode      `json:"properties,omitempty"`
	Required             []string                    `json:"required,omitempty"`
	Items                *schemaNode                 `json:"items,omitempty"`
	AdditionalProperties *schemaAdditionalProperties `json:"additionalProperties,omitempty"`
}

type schemaAdditionalProperties struct {
	Allowed bool
	Schema  *schemaNode
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

func Parse(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	all := &swaggerDoc{
		Definitions:         map[string]*schemaNode{},
		SecurityDefinitions: map[string]document.SecurityScheme{},
		Paths:               map[string]map[string]operation{},
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
		mergeDoc(all, &sw, src.Name, p)
	}
	return toRawIR(src.Name, all), nil
}

func applyEffectiveSecurity(doc *swaggerDoc) {
	if doc.Security == nil {
		return
	}
	security := doc.Security
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
	dst.Tags = document.MergeTags(dst.Tags, add.Tags, module, origin)
	document.MergeNamed(dst.Definitions, add.Definitions, "definition", module, origin)
	document.MergeNamed(dst.SecurityDefinitions, add.SecurityDefinitions, "security scheme", module, origin)
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

func toRawIR(name string, doc *swaggerDoc) *rawir.RawModule {
	mod := &rawir.RawModule{
		Name:    name,
		Schemas: make(map[string]*rawir.RawSchema, len(doc.Definitions)),
	}
	for k, v := range doc.Definitions {
		mod.Schemas[k] = convertSchema(v)
	}
	mod.GroupDescriptions = make(map[string]string, len(doc.Tags))
	for _, tag := range doc.Tags {
		mod.GroupDescriptions[tag.Name] = tag.Description
	}
	for path, methods := range doc.Paths {
		for _, m := range []string{"get", "post", "put", "delete", "patch"} {
			op, ok := methods[m]
			if !ok {
				continue
			}
			mod.Operations = append(mod.Operations, convertOp(op, m, path, doc.Produces, doc.Security, doc.SecurityDefinitions))
		}
	}
	return mod
}

func convertOp(op operation, method, path string, docProduces []string, globalSecurity []map[string][]string, schemes map[string]document.SecurityScheme) rawir.RawOperation {
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
			if !document.EqualJSON(existing, p) {
				fmt.Fprintf(os.Stderr, "warn: diverging duplicate parameter %q in %s %s (kept first)\n", p.Name, out.Method, path)
			}
			continue
		}
		seenParameters[key] = p
		if p.In == "body" {
			out.RequestBody = &rawir.RawRequestBody{Required: p.Required, Schema: convertSchema(p.Schema)}
			continue
		}
		out.Parameters = append(out.Parameters, rawir.RawParameter{
			Name:        p.Name,
			In:          p.In,
			Required:    p.Required,
			Type:        p.Type,
			Description: p.Description,
			Default:     document.String(p.Default),
			Enum:        document.Strings(p.Enum),
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
	out.Security = document.Security(sec, schemes)
	return out
}

func convertSchema(s *schemaNode) *rawir.RawSchema {
	if s == nil {
		return nil
	}
	out := &rawir.RawSchema{
		Ref:         s.Ref,
		Type:        s.Type,
		Description: s.Description,
		Format:      s.Format,
		Enum:        document.Strings(s.Enum),
		Nullable:    s.Nullable,
		ReadOnly:    s.ReadOnly,
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
	if s.AdditionalProperties != nil {
		out.AdditionalProperties = &rawir.RawAdditionalProperties{
			Allowed: s.AdditionalProperties.Allowed,
			Schema:  convertSchema(s.AdditionalProperties.Schema),
		}
	}
	return out
}
