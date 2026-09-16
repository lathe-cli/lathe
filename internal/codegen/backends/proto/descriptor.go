package proto

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
)

type index struct {
	messages map[string]*messageEntry
	enums    map[string]*descriptorpb.EnumDescriptorProto
}

type messageEntry struct {
	name    string
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
	idx.messages[full] = &messageEntry{name: full, file: file, msg: m, parents: append([]int32(nil), path...)}
	for i, nested := range m.NestedType {
		idx.indexMessage(file, full, nested, append(append([]int32(nil), path...), 3, int32(i)))
	}
	for _, e := range m.EnumType {
		idx.enums[full+"."+e.GetName()] = e
	}
}

func jsonName(f *descriptorpb.FieldDescriptorProto) string {
	if jn := f.GetJsonName(); jn != "" {
		return jn
	}
	return snakeToCamel(f.GetName())
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
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

func firstSentenceFromComment(file *descriptorpb.FileDescriptorProto, svc *descriptorpb.ServiceDescriptorProto, method *descriptorpb.MethodDescriptorProto) string {
	if file.SourceCodeInfo == nil {
		return ""
	}
	svcIdx := slices.Index(file.Service, svc)
	if svcIdx < 0 {
		return ""
	}
	methodIdx := slices.Index(svc.Method, method)
	if methodIdx < 0 {
		return ""
	}
	text := sourceComment(file, []int32{6, int32(svcIdx), 2, int32(methodIdx)})
	first, _, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(first)
}

func fieldComment(entry *messageEntry, field *descriptorpb.FieldDescriptorProto) string {
	if entry == nil || entry.file.SourceCodeInfo == nil {
		return ""
	}
	fieldIdx := slices.Index(entry.msg.Field, field)
	if fieldIdx < 0 {
		return ""
	}
	return sourceComment(entry.file, append(slices.Clone(entry.parents), 2, int32(fieldIdx)))
}

func sourceComment(file *descriptorpb.FileDescriptorProto, path []int32) string {
	for _, location := range file.GetSourceCodeInfo().GetLocation() {
		if slices.Equal(location.Path, path) {
			return strings.TrimSpace(location.GetLeadingComments())
		}
	}
	return ""
}
