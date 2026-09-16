package runtime

import (
	"io"
	"mime"
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
	disposition, dispositionParams, err := mime.ParseMediaType(got.disposition)
	testutil.Check(t, err == nil && disposition == "form-data" && dispositionParams["name"] == "file" && dispositionParams["filename"] == "sample.png", "Content-Disposition = %q: %v", got.disposition, err)
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
