package proto

import (
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"google.golang.org/protobuf/types/descriptorpb"
)

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
	typeName := entry.name
	if schema := wellKnownSchema(typeName); schema != nil {
		return schema
	}
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
			property := idx.fieldToSchema(f, out, visiting)
			property.Description = fieldComment(entry, f)
			sch.Properties[jsonName(f)] = property
		}
	}
	return &rawir.RawSchema{Ref: rawir.RefPrefix + key}
}

func schemaKey(fullTypeName string) string {
	return strings.TrimPrefix(fullTypeName, ".")
}

func (idx *index) bodyWildcardSchema(reqMsg *messageEntry, pathParamSet map[string]bool, out map[string]*rawir.RawSchema) *rawir.RawSchema {
	if reqMsg == nil {
		return nil
	}
	if schema := wellKnownSchema(reqMsg.name); schema != nil {
		return schema
	}
	schema := &rawir.RawSchema{Type: "object", Properties: map[string]*rawir.RawSchema{}}
	for _, f := range reqMsg.msg.Field {
		if pathParamSet[f.GetName()] {
			continue
		}
		property := idx.fieldToSchema(f, out, map[string]bool{})
		property.Description = fieldComment(reqMsg, f)
		schema.Properties[jsonName(f)] = property
	}
	if len(schema.Properties) == 0 {
		schema.Properties = nil
	}
	return schema
}

func (idx *index) fieldToSchema(f *descriptorpb.FieldDescriptorProto, out map[string]*rawir.RawSchema, visiting map[string]bool) *rawir.RawSchema {
	if idx.isMapField(f) {
		entry := idx.messages[f.GetTypeName()]
		value := findField(entry, "value")
		var valueSchema *rawir.RawSchema
		if value != nil {
			valueSchema = idx.fieldToSchema(value, out, visiting)
		}
		return &rawir.RawSchema{
			Type: "object", Nullable: true,
			AdditionalProperties: &rawir.RawAdditionalProperties{
				Allowed: true,
				Schema:  valueSchema,
			},
		}
	}
	repeated := f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	s := scalarOrMessageSchema(idx, f, out, visiting)
	if repeated {
		s.Nullable = false
		return &rawir.RawSchema{Type: "array", Items: s, Nullable: true}
	}
	s.Nullable = true
	return s
}

func scalarOrMessageSchema(idx *index, f *descriptorpb.FieldDescriptorProto, out map[string]*rawir.RawSchema, visiting map[string]bool) *rawir.RawSchema {
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		ref := f.GetTypeName()
		if schema := wellKnownSchema(ref); schema != nil {
			return schema
		}
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
		schema := &rawir.RawSchema{AnyOf: []*rawir.RawSchema{{Type: "string"}, {Type: "integer"}}}
		if enum := idx.enums[f.GetTypeName()]; enum != nil {
			for _, value := range enum.Value {
				schema.Enum = append(schema.Enum, value.GetName())
			}
		}
		return schema
	case descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32, descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		return &rawir.RawSchema{AnyOf: []*rawir.RawSchema{{Type: "integer"}, {Type: "string"}}}
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return &rawir.RawSchema{AnyOf: []*rawir.RawSchema{{Type: "number"}, {Type: "string"}}}
	default:
		return &rawir.RawSchema{Type: "string"}
	}
}

func wellKnownSchema(name string) *rawir.RawSchema {
	switch name {
	case ".google.protobuf.Timestamp", ".google.protobuf.Duration", ".google.protobuf.FieldMask":
		return &rawir.RawSchema{Type: "string"}
	case ".google.protobuf.Any", ".google.protobuf.Struct", ".google.protobuf.Empty":
		return &rawir.RawSchema{Type: "object"}
	case ".google.protobuf.Value":
		return &rawir.RawSchema{}
	case ".google.protobuf.ListValue":
		return &rawir.RawSchema{Type: "array"}
	case ".google.protobuf.StringValue", ".google.protobuf.BytesValue":
		return &rawir.RawSchema{Type: "string", Nullable: true}
	case ".google.protobuf.BoolValue":
		return &rawir.RawSchema{Type: "boolean", Nullable: true}
	case ".google.protobuf.Int32Value", ".google.protobuf.Int64Value", ".google.protobuf.UInt32Value", ".google.protobuf.UInt64Value":
		return &rawir.RawSchema{Nullable: true, AnyOf: []*rawir.RawSchema{{Type: "integer"}, {Type: "string"}}}
	case ".google.protobuf.FloatValue", ".google.protobuf.DoubleValue":
		return &rawir.RawSchema{Nullable: true, AnyOf: []*rawir.RawSchema{{Type: "number"}, {Type: "string"}}}
	default:
		return nil
	}
}
