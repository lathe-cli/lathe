package openapi3

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/lathe-cli/lathe/internal/codegen/backends/document"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

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
		mediaType, schema := contentSchema(op.RequestBody.Content, true)
		out.RequestBody = &rawir.RawRequestBody{Required: op.RequestBody.Required, MediaType: mediaType, Schema: schema}
	}
	for code, resp := range op.Responses {
		mediaType, schema := contentSchema(resp.Content, false)
		out.Responses[code] = &rawir.RawResponse{MediaType: mediaType, Schema: schema}
	}
	if r, ok := out.Responses["200"]; ok && r.MediaType != "" {
		out.Produces = []string{r.MediaType}
	}
	sec := globalSecurity
	if op.Security != nil {
		sec = *op.Security
	}
	out.Security = document.Security(sec)
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
		def = document.String(p.Schema.Default)
		enum = document.Strings(p.Schema.Enum)
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

func contentSchema(content map[string]mediaType, includeEmpty bool) (string, *rawir.RawSchema) {
	if media, ok := content["application/json"]; ok {
		return "application/json", convertSchema(media.Schema)
	}
	for _, name := range slices.Sorted(maps.Keys(content)) {
		if name != "" || includeEmpty {
			return name, convertSchema(content[name].Schema)
		}
	}
	return "", nil
}
