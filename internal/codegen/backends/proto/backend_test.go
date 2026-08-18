package proto

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestParse_Golden(t *testing.T) {
	cases := []struct {
		name  string
		build func() *descriptorpb.FileDescriptorSet
	}{
		{"google-api-http-get", buildGoogleAPIHTTPGet},
		{"google-api-http-post-body", buildGoogleAPIHTTPPostBody},
		{"google-api-http-post-body-star-path", buildGoogleAPIHTTPPostBodyStarPath},
		{"google-api-http-post-body-field", buildGoogleAPIHTTPPostBodyField},
		{"scalar-type-mapping", buildScalarTypeMapping},
		{"message-ref", buildMessageRef},
		{"no-http-rule", buildNoHTTPRule},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			syncDir := t.TempDir()
			data, err := proto.Marshal(tc.build())
			if err != nil {
				t.Fatalf("marshal FileDescriptorSet: %v", err)
			}
			if err := os.WriteFile(filepath.Join(syncDir, descriptorFile), data, 0o644); err != nil {
				t.Fatalf("seed descriptor_set.pb: %v", err)
			}

			src := &sourceconfig.Source{Name: "demo", Proto: &sourceconfig.ProtoConfig{Entries: []string{"demo.proto"}}}
			mod, err := Parse(src, syncDir)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			testutil.AssertRawModuleGolden(t, tc.name, mod)
		})
	}
}

func TestParseIgnoresImportedDependencyServices(t *testing.T) {
	fds := buildGoogleAPIHTTPGet()
	dependency := buildGoogleAPIHTTPGet().File[0]
	dependency.Name = proto.String("dependency.proto")
	fds.File = append(fds.File, dependency)
	data, err := proto.Marshal(fds)
	if err != nil {
		t.Fatal(err)
	}
	syncDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(syncDir, descriptorFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	src := &sourceconfig.Source{Name: "demo", Proto: &sourceconfig.ProtoConfig{Entries: []string{"demo.proto"}}}
	mod, err := Parse(src, syncDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(mod.Operations); got != 1 {
		t.Fatalf("operation count = %d, want only entry-file operations", got)
	}
}

func TestScalarOrMessageSchema_AcceptsStringEncodedProtoJSONIntegers(t *testing.T) {
	tests := []struct {
		name string
		typ  descriptorpb.FieldDescriptorProto_Type
	}{
		{name: "int32", typ: descriptorpb.FieldDescriptorProto_TYPE_INT32},
		{name: "int64", typ: descriptorpb.FieldDescriptorProto_TYPE_INT64},
		{name: "uint32", typ: descriptorpb.FieldDescriptorProto_TYPE_UINT32},
		{name: "uint64", typ: descriptorpb.FieldDescriptorProto_TYPE_UINT64},
		{name: "sint32", typ: descriptorpb.FieldDescriptorProto_TYPE_SINT32},
		{name: "sint64", typ: descriptorpb.FieldDescriptorProto_TYPE_SINT64},
		{name: "fixed32", typ: descriptorpb.FieldDescriptorProto_TYPE_FIXED32},
		{name: "fixed64", typ: descriptorpb.FieldDescriptorProto_TYPE_FIXED64},
		{name: "sfixed32", typ: descriptorpb.FieldDescriptorProto_TYPE_SFIXED32},
		{name: "sfixed64", typ: descriptorpb.FieldDescriptorProto_TYPE_SFIXED64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := scalarOrMessageSchema(&index{}, scalarField("value", 1, tc.typ), nil, nil)
			if schema.Type != "integer" {
				t.Fatalf("type = %q, want integer", schema.Type)
			}
			if !schema.AcceptStringEncodedInteger {
				t.Fatal("integer schema must accept quoted ProtoJSON numbers")
			}
		})
	}
}

func TestScalarOrMessageSchema_MapsProtoJSONWellKnownTypes(t *testing.T) {
	tests := []struct {
		name                       string
		typeName                   string
		wantType                   string
		wantStringEncodedInteger   bool
		wantStringEncodedNumber    bool
		wantUnconstrainedArrayItem bool
	}{
		{name: "timestamp", typeName: ".google.protobuf.Timestamp", wantType: "string"},
		{name: "duration", typeName: ".google.protobuf.Duration", wantType: "string"},
		{name: "field mask", typeName: ".google.protobuf.FieldMask", wantType: "string"},
		{name: "double wrapper", typeName: ".google.protobuf.DoubleValue", wantType: "number", wantStringEncodedNumber: true},
		{name: "float wrapper", typeName: ".google.protobuf.FloatValue", wantType: "number", wantStringEncodedNumber: true},
		{name: "int64 wrapper", typeName: ".google.protobuf.Int64Value", wantType: "integer", wantStringEncodedInteger: true},
		{name: "uint64 wrapper", typeName: ".google.protobuf.UInt64Value", wantType: "integer", wantStringEncodedInteger: true},
		{name: "int32 wrapper", typeName: ".google.protobuf.Int32Value", wantType: "integer", wantStringEncodedInteger: true},
		{name: "uint32 wrapper", typeName: ".google.protobuf.UInt32Value", wantType: "integer", wantStringEncodedInteger: true},
		{name: "bool wrapper", typeName: ".google.protobuf.BoolValue", wantType: "boolean"},
		{name: "string wrapper", typeName: ".google.protobuf.StringValue", wantType: "string"},
		{name: "bytes wrapper", typeName: ".google.protobuf.BytesValue", wantType: "string"},
		{name: "struct", typeName: ".google.protobuf.Struct", wantType: "object"},
		{name: "list value", typeName: ".google.protobuf.ListValue", wantType: "array", wantUnconstrainedArrayItem: true},
		{name: "value", typeName: ".google.protobuf.Value"},
		{name: "any", typeName: ".google.protobuf.Any", wantType: "object"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := scalarOrMessageSchema(&index{}, messageField("value", 1, tc.typeName), nil, nil)
			if schema.Type != tc.wantType {
				t.Fatalf("type = %q, want %q", schema.Type, tc.wantType)
			}
			if schema.AcceptStringEncodedInteger != tc.wantStringEncodedInteger {
				t.Fatalf("AcceptStringEncodedInteger = %v, want %v", schema.AcceptStringEncodedInteger, tc.wantStringEncodedInteger)
			}
			if schema.AcceptStringEncodedNumber != tc.wantStringEncodedNumber {
				t.Fatalf("AcceptStringEncodedNumber = %v, want %v", schema.AcceptStringEncodedNumber, tc.wantStringEncodedNumber)
			}
			if got := schema.Items != nil && schema.Items.Type == ""; got != tc.wantUnconstrainedArrayItem {
				t.Fatalf("unconstrained array item = %v, want %v", got, tc.wantUnconstrainedArrayItem)
			}
		})
	}
}

func TestBodyWildcardSchema_MapsProtoJSONWellKnownRequest(t *testing.T) {
	entry := &messageEntry{
		file: &descriptorpb.FileDescriptorProto{Package: proto.String("google.protobuf")},
		msg:  &descriptorpb.DescriptorProto{Name: proto.String("Timestamp")},
	}
	idx := &index{messages: map[string]*messageEntry{".google.protobuf.Timestamp": entry}}

	schema := idx.bodyWildcardSchema(entry, nil, map[string]*rawir.RawSchema{})
	if schema.Type != "string" {
		t.Fatalf("type = %q, want canonical ProtoJSON string", schema.Type)
	}
}

func TestBodyWildcardSchema_PreservesProtoJSONFieldNames(t *testing.T) {
	entry := &messageEntry{
		file: &descriptorpb.FileDescriptorProto{Package: proto.String("demo")},
		msg: &descriptorpb.DescriptorProto{
			Name:  proto.String("UpdateUserRequest"),
			Field: []*descriptorpb.FieldDescriptorProto{scalarField("user_id", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64)},
		},
	}
	idx := &index{messages: map[string]*messageEntry{".demo.UpdateUserRequest": entry}}

	schema := idx.bodyWildcardSchema(entry, nil, map[string]*rawir.RawSchema{})
	for _, name := range []string{"userId", "user_id"} {
		field := schema.Properties[name]
		if field == nil || field.Type != "integer" || !field.AcceptStringEncodedInteger {
			t.Fatalf("property %q = %#v, want ProtoJSON integer schema", name, field)
		}
	}
}

func TestFieldToSchema_PreservesProtoJSONFieldSemantics(t *testing.T) {
	idx := &index{}

	scalar := idx.fieldToSchema(scalarField("count", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32), nil, nil)
	if !scalar.Nullable {
		t.Fatal("scalar field must accept null as unset")
	}

	repeatedField := scalarField("counts", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32)
	repeatedField.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	repeated := idx.fieldToSchema(repeatedField, nil, nil)
	if !repeated.Nullable || repeated.Items == nil || repeated.Items.Nullable {
		t.Fatalf("repeated schema = %#v, want nullable field with non-nullable items", repeated)
	}

	enumField := scalarField("state", 1, descriptorpb.FieldDescriptorProto_TYPE_ENUM)
	enumField.TypeName = proto.String(".demo.State")
	enum := idx.fieldToSchema(enumField, nil, nil)
	if enum.Type != "string" || !enum.Nullable || !enum.AcceptIntegerEnum {
		t.Fatalf("enum schema = %#v, want nullable string accepting integer enum values", enum)
	}
	repeatedEnumField := proto.Clone(enumField).(*descriptorpb.FieldDescriptorProto)
	repeatedEnumField.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	repeatedEnum := idx.fieldToSchema(repeatedEnumField, nil, nil)
	if repeatedEnum.Items == nil || repeatedEnum.Items.Nullable {
		t.Fatalf("repeated enum schema = %#v, want non-nullable ordinary enum items", repeatedEnum)
	}
	repeatedNullValueField := proto.Clone(repeatedEnumField).(*descriptorpb.FieldDescriptorProto)
	repeatedNullValueField.TypeName = proto.String(".google.protobuf.NullValue")
	repeatedNullValue := idx.fieldToSchema(repeatedNullValueField, nil, nil)
	if repeatedNullValue.Items == nil || !repeatedNullValue.Items.Nullable {
		t.Fatalf("repeated NullValue schema = %#v, want nullable items", repeatedNullValue)
	}

	float := idx.fieldToSchema(scalarField("ratio", 1, descriptorpb.FieldDescriptorProto_TYPE_FLOAT), nil, nil)
	if float.Type != "number" || !float.Nullable || !float.AcceptStringEncodedNumber {
		t.Fatalf("float schema = %#v, want nullable number accepting ProtoJSON strings", float)
	}

	mapEntry := &descriptorpb.DescriptorProto{
		Name: proto.String("LabelsEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("value", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	}
	idx.messages = map[string]*messageEntry{
		".demo.LabelsEntry": {file: &descriptorpb.FileDescriptorProto{Package: proto.String("demo")}, msg: mapEntry},
	}
	mapSchema := idx.fieldToSchema(repeatedMessageField("labels", 1, ".demo.LabelsEntry"), map[string]*rawir.RawSchema{}, map[string]bool{})
	if mapSchema.Type != "object" || !mapSchema.Nullable || mapSchema.AdditionalProperties == nil || mapSchema.AdditionalProperties.Schema == nil || mapSchema.AdditionalProperties.Schema.Type != "string" {
		t.Fatalf("map schema = %#v, want nullable object with string values", mapSchema)
	}
	mapEntry.Field[1] = proto.Clone(repeatedNullValueField).(*descriptorpb.FieldDescriptorProto)
	mapEntry.Field[1].Name = proto.String("value")
	mapEntry.Field[1].Label = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	mapSchema = idx.fieldToSchema(repeatedMessageField("labels", 1, ".demo.LabelsEntry"), map[string]*rawir.RawSchema{}, map[string]bool{})
	if mapSchema.AdditionalProperties == nil || mapSchema.AdditionalProperties.Schema == nil || !mapSchema.AdditionalProperties.Schema.Nullable {
		t.Fatalf("NullValue map schema = %#v, want nullable values", mapSchema)
	}
}

// ---- descriptor builders ----------------------------------------------------

func scalarField(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(num),
		Type:   typ.Enum(),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
}

func messageField(name string, num int32, fullTypeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(num),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		TypeName: proto.String(fullTypeName),
	}
}

func repeatedMessageField(name string, num int32, fullTypeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(num),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		TypeName: proto.String(fullTypeName),
	}
}

func methodWithHTTP(name, in, out string, rule *annotations.HttpRule) *descriptorpb.MethodDescriptorProto {
	m := &descriptorpb.MethodDescriptorProto{
		Name:       proto.String(name),
		InputType:  proto.String(in),
		OutputType: proto.String(out),
	}
	if rule != nil {
		opts := &descriptorpb.MethodOptions{}
		proto.SetExtension(opts, annotations.E_Http, rule)
		m.Options = opts
	}
	return m
}

func fileSet(pkg string, msgs []*descriptorpb.DescriptorProto, svc *descriptorpb.ServiceDescriptorProto) *descriptorpb.FileDescriptorSet {
	file := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("demo.proto"),
		Package:     proto.String(pkg),
		Syntax:      proto.String("proto3"),
		MessageType: msgs,
		Service:     []*descriptorpb.ServiceDescriptorProto{svc},
	}
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}}
}

// ---- cases ------------------------------------------------------------------

func buildGoogleAPIHTTPGet() *descriptorpb.FileDescriptorSet {
	req := &descriptorpb.DescriptorProto{
		Name: proto.String("GetUserRequest"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	user := &descriptorpb.DescriptorProto{
		Name: proto.String("User"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Users"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"GetUser",
			".demo.GetUserRequest",
			".demo.User",
			&annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/users/{id}"}},
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{req, user}, svc)
}

func buildGoogleAPIHTTPPostBody() *descriptorpb.FileDescriptorSet {
	req := &descriptorpb.DescriptorProto{
		Name: proto.String("CreateUserRequest"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("email", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	user := &descriptorpb.DescriptorProto{
		Name: proto.String("User"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Users"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"CreateUser",
			".demo.CreateUserRequest",
			".demo.User",
			&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Post{Post: "/users"},
				Body:    "*",
			},
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{req, user}, svc)
}

func buildGoogleAPIHTTPPostBodyStarPath() *descriptorpb.FileDescriptorSet {
	labelsEntry := &descriptorpb.DescriptorProto{
		Name: proto.String("LabelsEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("value", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	}
	req := &descriptorpb.DescriptorProto{
		Name:       proto.String("UpdateUserRequest"),
		NestedType: []*descriptorpb.DescriptorProto{labelsEntry},
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("email", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			repeatedMessageField("labels", 4, ".demo.UpdateUserRequest.LabelsEntry"),
		},
	}
	user := &descriptorpb.DescriptorProto{
		Name: proto.String("User"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Users"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"UpdateUser",
			".demo.UpdateUserRequest",
			".demo.User",
			&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Post{Post: "/users/{id}"},
				Body:    "*",
			},
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{req, user}, svc)
}

func buildGoogleAPIHTTPPostBodyField() *descriptorpb.FileDescriptorSet {
	payload := &descriptorpb.DescriptorProto{
		Name: proto.String("CreateUserPayload"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("email", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	req := &descriptorpb.DescriptorProto{
		Name: proto.String("CreateUserRequest"),
		Field: []*descriptorpb.FieldDescriptorProto{
			messageField("user", 1, ".demo.CreateUserPayload"),
			scalarField("trace_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	user := &descriptorpb.DescriptorProto{
		Name: proto.String("User"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Users"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"CreateUser",
			".demo.CreateUserRequest",
			".demo.User",
			&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Post{Post: "/users"},
				Body:    "user",
			},
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{payload, req, user}, svc)
}

func buildScalarTypeMapping() *descriptorpb.FileDescriptorSet {
	req := &descriptorpb.DescriptorProto{
		Name: proto.String("ListXRequest"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			scalarField("count", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
			scalarField("big", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
			scalarField("flag", 4, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
			scalarField("blob", 5, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
		},
	}
	resp := &descriptorpb.DescriptorProto{
		Name: proto.String("ListXResponse"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("total", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32),
		},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Items"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"ListX",
			".demo.ListXRequest",
			".demo.ListXResponse",
			&annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/items"}},
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{req, resp}, svc)
}

func buildMessageRef() *descriptorpb.FileDescriptorSet {
	address := &descriptorpb.DescriptorProto{
		Name: proto.String("Address"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("street", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	req := &descriptorpb.DescriptorProto{
		Name: proto.String("GetUserRequest"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		},
	}
	user := &descriptorpb.DescriptorProto{
		Name: proto.String("User"),
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			messageField("address", 2, ".demo.Address"),
		},
	}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Users"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"GetUser",
			".demo.GetUserRequest",
			".demo.User",
			&annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/users/{id}"}},
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{address, req, user}, svc)
}

func buildNoHTTPRule() *descriptorpb.FileDescriptorSet {
	req := &descriptorpb.DescriptorProto{Name: proto.String("PingRequest")}
	resp := &descriptorpb.DescriptorProto{Name: proto.String("PingResponse")}
	svc := &descriptorpb.ServiceDescriptorProto{
		Name: proto.String("Health"),
		Method: []*descriptorpb.MethodDescriptorProto{methodWithHTTP(
			"Ping",
			".demo.PingRequest",
			".demo.PingResponse",
			nil,
		)},
	}
	return fileSet("demo", []*descriptorpb.DescriptorProto{req, resp}, svc)
}
