package openapi3

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/backends/document"
	"gopkg.in/yaml.v3"
)

type oas3Doc struct {
	Tags       []document.Tag        `json:"tags" yaml:"tags"`
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
	Schemas         map[string]*schemaNode             `json:"schemas,omitempty" yaml:"schemas,omitempty"`
	SecuritySchemes map[string]document.SecurityScheme `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
}

type pathItem struct {
	Parameters []parameter `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Servers    *[]server   `json:"servers,omitempty" yaml:"servers,omitempty"`
	Get        *operation  `json:"get,omitempty" yaml:"get,omitempty"`
	Post       *operation  `json:"post,omitempty" yaml:"post,omitempty"`
	Put        *operation  `json:"put,omitempty" yaml:"put,omitempty"`
	Delete     *operation  `json:"delete,omitempty" yaml:"delete,omitempty"`
	Patch      *operation  `json:"patch,omitempty" yaml:"patch,omitempty"`
	Head       *operation  `json:"head,omitempty" yaml:"head,omitempty"`
	Options    *operation  `json:"options,omitempty" yaml:"options,omitempty"`
	Trace      *operation  `json:"trace,omitempty" yaml:"trace,omitempty"`
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
	pathParams     []parameter
}

type parameter struct {
	Name          string      `json:"name" yaml:"name"`
	In            string      `json:"in" yaml:"in"`
	Required      bool        `json:"required" yaml:"required"`
	Schema        *schemaNode `json:"schema,omitempty" yaml:"schema,omitempty"`
	Description   string      `json:"description" yaml:"description"`
	Deprecated    bool        `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Style         string      `json:"style,omitempty" yaml:"style,omitempty"`
	Explode       *bool       `json:"explode,omitempty" yaml:"explode,omitempty"`
	AllowReserved bool        `json:"allowReserved,omitempty" yaml:"allowReserved,omitempty"`
}

type requestBody struct {
	Required bool                 `json:"required" yaml:"required"`
	Content  map[string]mediaType `json:"content" yaml:"content"`
}

type mediaType struct {
	Schema   *schemaNode         `json:"schema,omitempty" yaml:"schema,omitempty"`
	Encoding map[string]encoding `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

type encoding struct {
	ContentType string `json:"contentType,omitempty" yaml:"contentType,omitempty"`
}

type response struct {
	Content map[string]mediaType `json:"content,omitempty" yaml:"content,omitempty"`
}

type operationPath struct {
	path     string
	basePath string
}

type methodOperation struct {
	method string
	op     *operation
}

func (p *pathItem) operations() []methodOperation {
	return []methodOperation{{"GET", p.Get}, {"POST", p.Post}, {"PUT", p.Put}, {"DELETE", p.Delete}, {"PATCH", p.Patch}, {"HEAD", p.Head}, {"OPTIONS", p.Options}, {"TRACE", p.Trace}}
}

func applyServerBasePaths(doc *oas3Doc, module, origin string, override *string) {
	var rootBasePath string
	if override != nil {
		rootBasePath = *override
	} else {
		rootBasePath = resolveServerBasePath(doc.Servers, module, origin)
	}
	for _, path := range slices.Sorted(maps.Keys(doc.Paths)) {
		item := doc.Paths[path]
		basePath := rootBasePath
		if override == nil && item.Servers != nil {
			basePath = resolveServerBasePath(*item.Servers, module, origin)
		}
		for _, pair := range item.operations() {
			op := pair.op
			if op == nil {
				continue
			}
			op.serverBasePath = basePath
			if override == nil && op.Servers != nil {
				op.serverBasePath = resolveServerBasePath(*op.Servers, module, origin)
			}
		}
	}
}

func applyEffectiveSecurity(doc *oas3Doc) {
	if doc.Security == nil {
		return
	}
	security := doc.Security
	for _, path := range slices.Sorted(maps.Keys(doc.Paths)) {
		item := doc.Paths[path]
		for _, pair := range item.operations() {
			op := pair.op
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
	for _, name := range slices.Sorted(maps.Keys(servers[0].Variables)) {
		variable := servers[0].Variables[name]
		raw = strings.ReplaceAll(raw, "{"+name+"}", variable.Default)
	}
	basePath, err := document.ServerBasePath(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: %s: unsupported server URL %q in %s (ignored)\n", module, servers[0].URL, origin)
		return ""
	}
	return basePath
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

func mergeDoc(dst, add *oas3Doc, paths map[operationPath]map[string]*operation, module, origin string) error {
	dst.Tags = document.MergeTags(dst.Tags, add.Tags, module, origin)
	if add.Components != nil {
		if dst.Components == nil {
			dst.Components = &components{}
		}
		if dst.Components.Schemas == nil {
			dst.Components.Schemas = map[string]*schemaNode{}
		}
		if dst.Components.SecuritySchemes == nil {
			dst.Components.SecuritySchemes = map[string]document.SecurityScheme{}
		}
		document.MergeNamed(dst.Components.Schemas, add.Components.Schemas, "schema", module, origin)
		document.MergeNamed(dst.Components.SecuritySchemes, add.Components.SecuritySchemes, "security scheme", module, origin)
	}
	for _, path := range slices.Sorted(maps.Keys(add.Paths)) {
		item := add.Paths[path]
		for _, pair := range item.operations() {
			if pair.op == nil {
				continue
			}
			key := operationPath{path: path, basePath: pair.op.serverBasePath}
			if paths[key] == nil {
				paths[key] = map[string]*operation{}
			}
			if _, exists := paths[key][pair.method]; exists {
				return fmt.Errorf("%s: duplicate %s %s with base path %q in %s", module, pair.method, path, key.basePath, origin)
			}
			pair.op.pathParams = item.Parameters
			paths[key][pair.method] = pair.op
		}
	}
	return nil
}
