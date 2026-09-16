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
		t.Run(tc.name, func(t *testing.T) {
			mod := parseDescriptors(t, tc.build())
			testutil.AssertRawModuleGolden(t, tc.name, mod)
		})
	}
}

func TestParseIgnoresImportedDependencyServices(t *testing.T) {
	fds := buildGoogleAPIHTTPGet()
	dependency := buildGoogleAPIHTTPGet().File[0]
	dependency.Name = proto.String("dependency.proto")
	fds.File = append(fds.File, dependency)
	mod := parseDescriptors(t, fds)
	if got := len(mod.Operations); got != 1 {
		t.Fatalf("operation count = %d, want only entry-file operations", got)
	}
}

func TestParsePreservesMapRequestBodySchema(t *testing.T) {
	fds := buildGoogleAPIHTTPPostBodyStarPath()
	mod := parseDescriptors(t, fds)
	labels := mod.Operations[0].RequestBody.Schema.Properties["labels"]
	testutil.Require(t, labels != nil && labels.Type == "object" && labels.AdditionalProperties != nil && labels.AdditionalProperties.Schema != nil && labels.AdditionalProperties.Schema.Type == "string", "labels schema = %#v", labels)
}

func TestParsePreservesDescriptorComments(t *testing.T) {
	descriptors := buildGoogleAPIHTTPGet()
	descriptors.File[0].SourceCodeInfo = &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{
		{Path: []int32{6, 0, 2, 0}, LeadingComments: proto.String("\n Get one user.\nAdditional details.\n")},
		{Path: []int32{4, 0, 2, 0}, LeadingComments: proto.String(" User identifier. ")},
		{Path: []int32{4, 1, 2, 1}, LeadingComments: proto.String(" Display name. ")},
	}}
	mod := parseDescriptors(t, descriptors)
	operation := mod.Operations[0]
	testutil.Require(t, operation.Summary == "Get one user." && operation.Parameters[0].Description == "User identifier.", "operation comments = %#v", operation)
	if name := mod.Schemas["demo.User"].Properties["name"]; name.Description != "Display name." {
		t.Fatalf("response field comment = %#v", name)
	}
}

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
	field := messageField(name, num, fullTypeName)
	field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return field
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

func buildGoogleAPIHTTPGet() *descriptorpb.FileDescriptorSet {
	req := message("GetUserRequest",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	user := message("User",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
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
	req := message("CreateUserRequest",
		scalarField("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("email", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	user := message("User",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
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
	labelsEntry := message("LabelsEntry",
		scalarField("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("value", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	labelsEntry.Options = &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}
	req := message("UpdateUserRequest",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("email", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		repeatedMessageField("labels", 4, ".demo.UpdateUserRequest.LabelsEntry"),
	)
	req.NestedType = []*descriptorpb.DescriptorProto{labelsEntry}
	user := message("User",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
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
	payload := message("CreateUserPayload",
		scalarField("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("email", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	req := message("CreateUserRequest",
		messageField("user", 1, ".demo.CreateUserPayload"),
		scalarField("trace_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	user := message("User",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
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
	req := message("ListXRequest",
		scalarField("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		scalarField("count", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
		scalarField("big", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
		scalarField("flag", 4, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
		scalarField("blob", 5, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
	)
	resp := message("ListXResponse",
		scalarField("total", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32),
	)
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
	address := message("Address",
		scalarField("street", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	req := message("GetUserRequest",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
	)
	user := message("User",
		scalarField("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
		messageField("address", 2, ".demo.Address"),
	)
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

func parseDescriptors(t *testing.T, descriptors *descriptorpb.FileDescriptorSet) *rawir.RawModule {
	t.Helper()
	data, err := proto.Marshal(descriptors)
	testutil.Require(t, err == nil, "%v", err)
	dir := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, descriptorFile), data, 0o644))
	mod, err := Parse(&sourceconfig.Source{Name: "demo", Proto: &sourceconfig.ProtoConfig{Entries: []string{"demo.proto"}}}, dir)
	testutil.Require(t, err == nil, "%v", err)
	return mod
}

func message(name string, fields ...*descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{Name: proto.String(name), Field: fields}
}
