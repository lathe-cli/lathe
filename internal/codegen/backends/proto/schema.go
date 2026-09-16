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
			Type: "object",
			AdditionalProperties: &rawir.RawAdditionalProperties{
				Allowed: true,
				Schema:  valueSchema,
			},
		}
	}
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
		schema := &rawir.RawSchema{Type: "string"}
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
		return &rawir.RawSchema{Type: "integer"}
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return &rawir.RawSchema{Type: "number"}
	default:
		return &rawir.RawSchema{Type: "string"}
	}
}
