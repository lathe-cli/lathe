package proto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

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

			src := &sourceconfig.Source{Name: "demo"}
			mod, err := Parse(src, syncDir)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			sort.Slice(mod.Operations, func(i, j int) bool {
				if mod.Operations[i].Path != mod.Operations[j].Path {
					return mod.Operations[i].Path < mod.Operations[j].Path
				}
				return mod.Operations[i].Method < mod.Operations[j].Method
			})

			out, err := json.MarshalIndent(mod, "", "  ")
			if err != nil {
				t.Fatalf("marshal rawir: %v", err)
			}
			out = append(out, '\n')
			testutil.AssertGolden(t, filepath.Join("testdata", tc.name+".golden.json"), out)
		})
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
