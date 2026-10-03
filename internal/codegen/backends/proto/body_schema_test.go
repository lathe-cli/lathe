package proto

import (
	"context"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/runtime"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestGeneratedBodySchemaPreservesProtoJSON(t *testing.T) {
	fds := buildGoogleAPIHTTPPostBody()
	request := fds.File[0].MessageType[0]
	request.Field = []*descriptorpb.FieldDescriptorProto{
		scalarField("count", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64),
		scalarField("ratio", 2, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
		scalarField("enabled", 3, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
		scalarField("names", 4, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	}
	request.Field[3].Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	fds.File[0].EnumType = []*descriptorpb.EnumDescriptorProto{{Name: proto.String("Color"), Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("RED"), Number: proto.Int32(0)}, {Name: proto.String("BLUE"), Number: proto.Int32(1)}}}}
	color := scalarField("color", 20, descriptorpb.FieldDescriptorProto_TYPE_ENUM)
	color.TypeName = proto.String(".demo.Color")
	request.Field = append(request.Field, color)
	for i, msg := range []proto.Message{&timestamppb.Timestamp{}, &durationpb.Duration{}, &fieldmaskpb.FieldMask{}, &wrapperspb.Int64Value{}, &structpb.Struct{}, &structpb.Value{}, &structpb.ListValue{}, &anypb.Any{}} {
		descriptor := msg.ProtoReflect().Descriptor()
		file := protodesc.ToFileDescriptorProto(descriptor.ParentFile())
		present := false
		for _, name := range fds.File[0].Dependency {
			present = present || name == file.GetName()
		}
		if !present {
			fds.File[0].Dependency = append(fds.File[0].Dependency, file.GetName())
			fds.File = append(fds.File, file)
		}
		name := []string{"time", "duration", "mask", "wrapper", "object", "value", "list", "any"}[i]
		request.Field = append(request.Field, messageField(name, int32(i+5), "."+string(descriptor.FullName())))
	}
	file, err := protodesc.NewFile(fds.File[0], protoregistry.GlobalFiles)
	testutil.NoError(t, err)
	spec := normalize.Normalize(parseDescriptors(t, fds))[0]
	for _, body := range []string{
		`{"count":"9223372036854775807","ratio":"NaN","enabled":null,"names":null}`,
		`{"time":"2026-01-01T00:00:00Z","duration":"1s","mask":"fooBar","wrapper":"7","object":{"count":true},"value":"ok","list":[1,null],"any":{"@type":"type.googleapis.com/google.protobuf.Int64Value","value":"7"}}`,
		`{"count":7,"ratio":0.5,"enabled":true,"names":["a"]}`,
		`{"color":1}`,
		`{"color":"BLUE"}`,
	} {
		testutil.NoError(t, protojson.Unmarshal([]byte(body), dynamicpb.NewMessage(file.Messages().Get(0))))
		_, err := runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{HasFile: true, FileBody: []byte(body)}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
		testutil.NoError(t, err)
	}
	for _, body := range []string{`{"enabled":"yes"}`, `{"names":[null]}`, `{"time":{}}`} {
		_, err := runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{HasFile: true, FileBody: []byte(body)}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
		testutil.Require(t, err != nil, "invalid ProtoJSON structure accepted: %s", body)
	}
}

func TestGeneratedProtoJSONBodyFlags(t *testing.T) {
	fds := buildGoogleAPIHTTPPostBody()
	fds.File[0].MessageType[0].Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()
	spec := normalize.Normalize(parseDescriptors(t, fds))[0]
	params, _, err := normalize.ExpandJSONBodyFlags(spec)
	testutil.NoError(t, err)
	spec.Params = params
	_, err = runtime.InvokeOperation(context.Background(), spec, runtime.OperationInput{Values: map[string]any{"name": int64(7)}, Changed: map[string]bool{"name": true}}, runtime.OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
	testutil.NoError(t, err)
}
