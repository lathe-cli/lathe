package proto

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
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
	mod := &rawir.RawModule{
		Name:    src.Name,
		Schemas: map[string]*rawir.RawSchema{},
	}

	for _, file := range fds.File {
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

type index struct {
	messages map[string]*messageEntry
	enums    map[string]*descriptorpb.EnumDescriptorProto
}

type messageEntry struct {
	file    *descriptorpb.FileDescriptorProto
	msg     *descriptorpb.DescriptorProto
	parents []int32
}

func newIndex(fds *descriptorpb.FileDescriptorSet) *index {
	idx := &index{
		messages: map[string]*messageEntry{},
		enums:    map[string]*descriptorpb.EnumDescriptorProto{},
	}
	for _, file := range fds.File {
		pkg := file.GetPackage()
		for i, m := range file.MessageType {
			idx.indexMessage(file, "."+pkg, m, []int32{4, int32(i)})
		}
		for _, e := range file.EnumType {
			idx.enums["."+pkg+"."+e.GetName()] = e
		}
	}
	return idx
}

func (idx *index) indexMessage(file *descriptorpb.FileDescriptorProto, parent string, m *descriptorpb.DescriptorProto, path []int32) {
	full := parent + "." + m.GetName()
	idx.messages[full] = &messageEntry{file: file, msg: m, parents: append([]int32(nil), path...)}
	for i, nested := range m.NestedType {
		idx.indexMessage(file, full, nested, append(append([]int32(nil), path...), 3, int32(i)))
	}
	for _, e := range m.EnumType {
		idx.enums[full+"."+e.GetName()] = e
	}
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
		}
		op.RequestBody = &rawir.RawRequestBody{Required: true, Schema: schema}
	}

	if reqMsg != nil {
		bodyRoot := rule.body
		bodyAll := bodyRoot == "*"
		for _, f := range reqMsg.msg.Field {
			rawName := f.GetName()
			if pathParamSet[rawName] {
				continue
			}
			if bodyAll {
				continue
			}
			if bodyRoot != "" && rawName == bodyRoot {
				continue
			}
			if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE &&
				f.GetLabel() != descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
				continue
			}
			if idx.isMapField(f) {
				continue
			}
			qtype := queryType(f)
			if qtype == "" {
				continue
			}
			op.Parameters = append(op.Parameters, rawir.RawParameter{
				Name:        jsonName(f),
				In:          "query",
				Required:    false,
				Type:        qtype,
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
		name := inner
		if i := strings.Index(inner, "="); i >= 0 {
			name = inner[:i]
		}
		names = append(names, name)
		root := rootOf(name)
		return "{" + root + "}"
	})
	return names, cleaned
}

func jsonName(f *descriptorpb.FieldDescriptorProto) string {
	if jn := f.GetJsonName(); jn != "" {
		return jn
	}
	return snakeToCamel(f.GetName())
}

func snakeToCamel(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return parts[0]
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

func (idx *index) isMapField(f *descriptorpb.FieldDescriptorProto) bool {
	if f.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		return false
	}
	if f.GetLabel() != descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		return false
	}
	target := idx.messages[f.GetTypeName()]
	if target == nil {
		return false
	}
	return target.msg.GetOptions().GetMapEntry()
}

func rootOf(pathExpr string) string {
	if i := strings.Index(pathExpr, "."); i >= 0 {
		return pathExpr[:i]
	}
	return pathExpr
}

func findField(m *messageEntry, name string) *descriptorpb.FieldDescriptorProto {
	if m == nil {
		return nil
	}
	for _, f := range m.msg.Field {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func queryType(f *descriptorpb.FieldDescriptorProto) string {
	repeated := f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	if repeated {
		return "array"
	}
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return "boolean"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		return "integer"
	default:
		return "string"
	}
}

func (idx *index) messageToSchema(entry *messageEntry, out map[string]*rawir.RawSchema, visiting map[string]bool) *rawir.RawSchema {
	if entry == nil {
		return nil
	}
	typeName := idx.fullNameOf(entry)
	if visiting[typeName] {
		return &rawir.RawSchema{Ref: rawir.RefPrefix + schemaKey(typeName)}
	}
	visiting[typeName] = true
	defer delete(visiting, typeName)

	key := schemaKey(typeName)
	if _, exists := out[key]; !exists {
		sch := &rawir.RawSchema{
			Type:       "object",
			Properties: map[string]*rawir.RawSchema{},
		}
		out[key] = sch
		for _, f := range entry.msg.Field {
			if idx.isMapField(f) {
				continue
			}
			sch.Properties[jsonName(f)] = idx.fieldToSchema(f, out, visiting)
		}
	}
	return &rawir.RawSchema{Ref: rawir.RefPrefix + key}
}

func (idx *index) fullNameOf(entry *messageEntry) string {
	for k, v := range idx.messages {
		if v == entry {
			return k
		}
	}
	return "." + entry.file.GetPackage() + "." + entry.msg.GetName()
}

func schemaKey(fullTypeName string) string {
	return strings.TrimPrefix(fullTypeName, ".")
}

func (idx *index) bodyWildcardSchema(reqMsg *messageEntry, pathParamSet map[string]bool, out map[string]*rawir.RawSchema) *rawir.RawSchema {
	if reqMsg == nil {
		return nil
	}
	schema := &rawir.RawSchema{Type: "object", Properties: map[string]*rawir.RawSchema{}}
	for _, f := range reqMsg.msg.Field {
		if pathParamSet[f.GetName()] {
			continue
		}
		if idx.isMapField(f) {
			continue
		}
		schema.Properties[jsonName(f)] = idx.fieldToSchema(f, out, map[string]bool{})
	}
	if len(schema.Properties) == 0 {
		schema.Properties = nil
	}
	return schema
}

func (idx *index) fieldToSchema(f *descriptorpb.FieldDescriptorProto, out map[string]*rawir.RawSchema, visiting map[string]bool) *rawir.RawSchema {
	repeated := f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	s := scalarOrMessageSchema(idx, f, out, visiting)
	if repeated {
		return &rawir.RawSchema{Type: "array", Items: s}
	}
	return s
}

func scalarOrMessageSchema(idx *index, f *descriptorpb.FieldDescriptorProto, out map[string]*rawir.RawSchema, visiting map[string]bool) *rawir.RawSchema {
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		ref := f.GetTypeName()
		target := idx.messages[ref]
		if target == nil {
			return &rawir.RawSchema{Type: "object"}
		}
		return idx.messageToSchema(target, out, visiting)
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return &rawir.RawSchema{Type: "boolean"}
	case descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return &rawir.RawSchema{Type: "string"}
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		return &rawir.RawSchema{Type: "string"}
	case descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32, descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		return &rawir.RawSchema{Type: "integer"}
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return &rawir.RawSchema{Type: "number"}
	default:
		return &rawir.RawSchema{Type: "string"}
	}
}

func firstSentenceFromComment(file *descriptorpb.FileDescriptorProto, svc *descriptorpb.ServiceDescriptorProto, method *descriptorpb.MethodDescriptorProto) string {
	loc := findMethodComment(file, svc, method)
	if loc == nil {
		return ""
	}
	s := strings.TrimSpace(loc.GetLeadingComments())
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func findMethodComment(file *descriptorpb.FileDescriptorProto, svc *descriptorpb.ServiceDescriptorProto, method *descriptorpb.MethodDescriptorProto) *descriptorpb.SourceCodeInfo_Location {
	if file.SourceCodeInfo == nil {
		return nil
	}
	svcIdx := -1
	for i, s := range file.Service {
		if s == svc {
			svcIdx = i
			break
		}
	}
	if svcIdx < 0 {
		return nil
	}
	methodIdx := -1
	for i, m := range svc.Method {
		if m == method {
			methodIdx = i
			break
		}
	}
	if methodIdx < 0 {
		return nil
	}
	target := []int32{6, int32(svcIdx), 2, int32(methodIdx)}
	for _, loc := range file.SourceCodeInfo.Location {
		if pathEqual(loc.Path, target) {
			return loc
		}
	}
	return nil
}

func fieldComment(entry *messageEntry, field *descriptorpb.FieldDescriptorProto) string {
	if entry == nil || entry.file.SourceCodeInfo == nil {
		return ""
	}
	fieldIdx := -1
	for i, f := range entry.msg.Field {
		if f == field {
			fieldIdx = i
			break
		}
	}
	if fieldIdx < 0 {
		return ""
	}
	target := append(append([]int32(nil), entry.parents...), 2, int32(fieldIdx))
	for _, loc := range entry.file.SourceCodeInfo.Location {
		if pathEqual(loc.Path, target) {
			return strings.TrimSpace(loc.GetLeadingComments())
		}
	}
	return ""
}

func pathEqual(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
