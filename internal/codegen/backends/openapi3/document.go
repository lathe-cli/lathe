package openapi3

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/backends/document"
	"gopkg.in/yaml.v3"
)

type oas3Doc struct {
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
	if doc.Security == nil {
		return
	}
	security := doc.Security
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
				if !document.EqualJSON(existing, v) {
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
		existing.Get = cmp.Or(existing.Get, item.Get)
		existing.Post = cmp.Or(existing.Post, item.Post)
		existing.Put = cmp.Or(existing.Put, item.Put)
		existing.Delete = cmp.Or(existing.Delete, item.Delete)
		existing.Patch = cmp.Or(existing.Patch, item.Patch)
	}
}
