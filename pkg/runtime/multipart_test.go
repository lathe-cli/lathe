package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_MultipartSendsFileAndFields(t *testing.T) {
	isolateRuntime(t)

	type capture struct {
		contentType string
		disposition string
		filename    string
		fileType    string
		fileBody    string
		queryValue  string
		bodyValue   string
		err         error
	}
	var got capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.contentType = r.Header.Get("Content-Type")
		got.queryValue = r.URL.Query().Get("purpose")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			got.err = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		got.bodyValue = strings.Join(r.MultipartForm.Value["purpose"], ",")
		file, header, err := r.FormFile("file")
		if err != nil {
			got.err = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			got.err = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		got.disposition = header.Header.Get("Content-Disposition")
		got.filename = header.Filename
		got.fileType = header.Header.Get("Content-Type")
		got.fileBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"file-1"}`))
	}))
	defer srv.Close()

	filePath := t.TempDir() + "/sample.png"
	testutil.NoError(t, os.WriteFile(filePath, []byte("file-content"), 0o600))

	root := newExecutionRoot("raw")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Uploads",
		Use:     "create",
		Method:  http.MethodPost,
		PathTpl: "/uploads",
		Params: []ParamSpec{
			{Name: "purpose", Flag: "purpose", In: InQuery, GoType: "string"},
			{Name: "file", Flag: "file", In: InFormData, GoType: "string", Required: true, Format: "binary"},
			{Name: "purpose", Flag: "body-purpose", In: InFormData, GoType: "string"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "multipart/form-data"},
		Security:    &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "uploads", "create", "--file", filePath, "--purpose", "query", "--body-purpose", "body"})

	testutil.NoError(t, root.Execute())
	testutil.Require(t, got.err == nil, "parse multipart: %v", got.err)
	testutil.Check(t, strings.HasPrefix(got.contentType, "multipart/form-data; boundary="), "Content-Type = %q", got.contentType)
	testutil.Check(t, got.filename == "sample.png" && got.fileType == "image/png" && got.fileBody == "file-content", "file = filename %q, type %q, body %q", got.filename, got.fileType, got.fileBody)
	testutil.Check(t, got.disposition == "form-data; name=\"file\"; filename=\"sample.png\"", "Content-Disposition = %q", got.disposition)
	testutil.Check(t, got.queryValue == "query" && got.bodyValue == "body", "purpose = query %q, body %q", got.queryValue, got.bodyValue)
}

func TestBuild_MultipartFileErrorPrecedesAuth(t *testing.T) {
	isolateRuntime(t)

	root := newExecutionRoot("raw")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{{
		Group: "Uploads", Use: "create", Method: http.MethodPost, PathTpl: "/uploads",
		Params:      []ParamSpec{{Name: "file", Flag: "file", In: InFormData, GoType: "string", Required: true, Format: "binary"}},
		RequestBody: &RequestBody{Required: true, MediaType: "multipart/form-data"},
	}})
	root.SetArgs([]string{"--hostname", "https://example.invalid", "demo", "uploads", "create", "--file", t.TempDir() + "/missing"})

	err := root.Execute()
	testutil.Require(t, err != nil && ClassifyError(err).Code == CodeUsage && strings.Contains(err.Error(), "read multipart file"), "error = %v, want local multipart usage error before auth", err)
}

func TestMultipartPartContentTypeSelection(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nrest")
	text := []byte("hello")
	for _, tc := range []struct {
		name        string
		contentType string
		binary      bool
		body        []byte
		wantType    string
		wantHeader  bool
	}{
		{name: "wildcard png", contentType: "image/*", binary: true, body: png, wantType: "image/png", wantHeader: true},
		{name: "wildcard text", contentType: "image/*", binary: true, body: text, wantType: "application/octet-stream", wantHeader: true},
		{name: "list text bytes", contentType: "image/png, image/jpeg", binary: true, body: text, wantType: "image/png", wantHeader: true},
		{name: "json text", contentType: "application/json", body: []byte(`{"k":1}`), wantType: "application/json", wantHeader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			param := ParamSpec{Name: "part", Flag: "part", In: InFormData, GoType: "string", Required: true, ContentType: tc.contentType}
			value := any(string(tc.body))
			if tc.binary {
				param.Format = "binary"
				path := t.TempDir() + "/part.bin"
				testutil.NoError(t, os.WriteFile(path, tc.body, 0o600))
				value = path
			}
			var gotType string
			var gotHeader bool
			var gotDisposition string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reader, err := r.MultipartReader()
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				part, err := reader.NextPart()
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				gotHeader = part.Header.Get("Content-Type") != ""
				gotType = part.Header.Get("Content-Type")
				gotDisposition = part.Header.Get("Content-Disposition")
				if err := part.Close(); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			_, err := InvokeOperation(context.Background(), CommandSpec{
				Method:      http.MethodPost,
				PathTpl:     "/uploads",
				Params:      []ParamSpec{param},
				RequestBody: &RequestBody{Required: true, MediaType: "multipart/form-data"},
			}, OperationInput{Values: map[string]any{"part": value}}, OperationOptions{Hostname: srv.URL})
			testutil.NoError(t, err)
			testutil.Require(t, gotHeader == tc.wantHeader && gotType == tc.wantType, "header %v type %q, want header %v type %q", gotHeader, gotType, tc.wantHeader, tc.wantType)
			wantDisposition := "form-data; name=\"part\""
			if tc.binary {
				wantDisposition = "form-data; name=\"part\"; filename=\"part.bin\""
			}
			testutil.Require(t, gotDisposition == wantDisposition, "disposition = %q, want %q", gotDisposition, wantDisposition)
		})
	}
}

func TestInvokeOperation_MultipartRejectsJSONBodyFile(t *testing.T) {
	_, err := InvokeOperation(context.Background(), CommandSpec{
		Method:      http.MethodPost,
		PathTpl:     "/uploads",
		RequestBody: &RequestBody{Required: true, MediaType: "multipart/form-data"},
	}, OperationInput{HasFile: true, FileBody: []byte(`{}`)}, OperationOptions{Hostname: "http://127.0.0.1:1", DryRun: true})
	testutil.Require(t, err != nil && ClassifyError(err).Code == CodeUsage && ClassifyError(err).ExitCode == ExitUsage && strings.Contains(err.Error(), "multipart request bodies accept only part flags"), "error = %v", err)
}

func TestContentDispositionEscapesHeaderBreaks(t *testing.T) {
	got := contentDisposition("a\"\r\nX-Evil: 1", "f\\\n.png")
	testutil.Require(t, got == `form-data; name="a\"%0D%0AX-Evil: 1"; filename="f\\%0A.png"`, "Content-Disposition = %q", got)
}
