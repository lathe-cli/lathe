package proto

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const descriptorFile = "descriptor_set.pb"

func Parse(src *sourceconfig.Source, syncDir string) (*rawir.RawModule, error) {
	data, err := os.ReadFile(filepath.Join(syncDir, descriptorFile))
	if err != nil {
		return nil, fmt.Errorf("read descriptor_set.pb: %w", err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &fds); err != nil {
		return nil, fmt.Errorf("unmarshal descriptor_set.pb: %w", err)
	}

	idx := newIndex(&fds)
	entries := make(map[string]bool, len(src.Proto.Entries))
	for _, entry := range src.Proto.Entries {
		entries[entry] = true
	}
	mod := &rawir.RawModule{
		Name:    src.Name,
		Schemas: map[string]*rawir.RawSchema{},
	}

	for _, file := range fds.File {
		if !entries[file.GetName()] {
			continue
		}
		for _, svc := range file.Service {
			for _, m := range svc.Method {
				rules := extractHTTPRules(m)
				for _, rule := range rules {
					op, extraSchemas, ok := idx.buildOperation(file, svc, m, rule)
					if !ok {
						continue
					}
					mod.Operations = append(mod.Operations, op)
					for k, v := range extraSchemas {
						if _, exists := mod.Schemas[k]; !exists {
							mod.Schemas[k] = v
						}
					}
				}
			}
		}
	}
	return mod, nil
}

type httpPattern struct {
	method string
	path   string
	body   string
}

func extractHTTPRules(m *descriptorpb.MethodDescriptorProto) []httpPattern {
	if m.Options == nil {
		return nil
	}
	if !proto.HasExtension(m.Options, annotations.E_Http) {
		return nil
	}
	raw := proto.GetExtension(m.Options, annotations.E_Http)
	rule, ok := raw.(*annotations.HttpRule)
	if !ok || rule == nil {
		return nil
	}
	var out []httpPattern
	if p, ok := patternOf(rule); ok {
		out = append(out, p)
	}
	for _, ab := range rule.AdditionalBindings {
		if p, ok := patternOf(ab); ok {
			out = append(out, p)
		}
	}
	return out
}

func patternOf(r *annotations.HttpRule) (httpPattern, bool) {
	p := httpPattern{body: r.Body}
	switch v := r.Pattern.(type) {
	case *annotations.HttpRule_Get:
		p.method, p.path = "GET", v.Get
	case *annotations.HttpRule_Put:
		p.method, p.path = "PUT", v.Put
	case *annotations.HttpRule_Post:
		p.method, p.path = "POST", v.Post
	case *annotations.HttpRule_Delete:
		p.method, p.path = "DELETE", v.Delete
	case *annotations.HttpRule_Patch:
		p.method, p.path = "PATCH", v.Patch
	case *annotations.HttpRule_Custom:
		if v.Custom == nil {
			return p, false
		}
		p.method, p.path = strings.ToUpper(v.Custom.Kind), v.Custom.Path
	default:
		return p, false
	}
	return p, true
}

var pathVarRE = regexp.MustCompile(`\{([^{}]+)\}`)

func (idx *index) buildOperation(
	file *descriptorpb.FileDescriptorProto,
	svc *descriptorpb.ServiceDescriptorProto,
	method *descriptorpb.MethodDescriptorProto,
	rule httpPattern,
) (rawir.RawOperation, map[string]*rawir.RawSchema, bool) {
	if rule.path == "" || rule.method == "" {
		return rawir.RawOperation{}, nil, false
	}
	reqMsg := idx.messages[method.GetInputType()]
	respMsg := idx.messages[method.GetOutputType()]

	pathParamNames, cleanedPath := parsePathVars(rule.path)
	pathParamSet := map[string]bool{}
	for _, n := range pathParamNames {
		pathParamSet[rootOf(n)] = true
	}
	for _, n := range pathParamNames {
		rootName := rootOf(n)
		if fieldDesc := findField(reqMsg, rootName); fieldDesc != nil {
			jn := jsonName(fieldDesc)
			if jn != rootName {
				cleanedPath = strings.ReplaceAll(cleanedPath, "{"+rootName+"}", "{"+jn+"}")
			}
		}
	}

	op := rawir.RawOperation{
		Group:       svc.GetName(),
		OperationID: svc.GetName() + "_" + method.GetName(),
		Summary:     firstSentenceFromComment(file, svc, method),
		Method:      rule.method,
		Path:        cleanedPath,
		Responses:   map[string]*rawir.RawResponse{},
	}

	for _, name := range pathParamNames {
		rootName := rootOf(name)
		displayName := rootName
		desc := ""
		if fieldDesc := findField(reqMsg, rootName); fieldDesc != nil {
			displayName = jsonName(fieldDesc)
			desc = fieldComment(reqMsg, fieldDesc)
		}
		op.Parameters = append(op.Parameters, rawir.RawParameter{
			Name:        displayName,
			In:          "path",
			Required:    true,
			Type:        "string",
			Description: desc,
		})
	}

	extra := map[string]*rawir.RawSchema{}
	if rule.body != "" && reqMsg != nil {
		var schema *rawir.RawSchema
		if rule.body == "*" {
			schema = idx.bodyWildcardSchema(reqMsg, pathParamSet, extra)
		} else if field := findField(reqMsg, rule.body); field != nil {
			schema = idx.fieldToSchema(field, extra, map[string]bool{})
			schema.Description = fieldComment(reqMsg, field)
		}
		op.RequestBody = &rawir.RawRequestBody{Required: true, Schema: schema}
	}

	if reqMsg != nil && rule.body != "*" {
		for _, f := range reqMsg.msg.Field {
			rawName := f.GetName()
			if pathParamSet[rawName] || rule.body != "" && rawName == rule.body || idx.isMapField(f) ||
				f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE && f.GetLabel() != descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
				continue
			}
			op.Parameters = append(op.Parameters, rawir.RawParameter{
				Name:        jsonName(f),
				In:          "query",
				Required:    false,
				Type:        queryType(f),
				Description: fieldComment(reqMsg, f),
			})
		}
	}

	if respMsg != nil {
		respSchema := idx.messageToSchema(respMsg, extra, map[string]bool{})
		op.Responses["200"] = &rawir.RawResponse{Schema: respSchema}
	}

	return op, extra, true
}

func parsePathVars(pattern string) ([]string, string) {
	var names []string
	cleaned := pathVarRE.ReplaceAllStringFunc(pattern, func(s string) string {
		inner := s[1 : len(s)-1]
		name, _, _ := strings.Cut(inner, "=")
		names = append(names, name)
		root := rootOf(name)
		return "{" + root + "}"
	})
	return names, cleaned
}

func rootOf(pathExpr string) string {
	root, _, _ := strings.Cut(pathExpr, ".")
	return root
}
