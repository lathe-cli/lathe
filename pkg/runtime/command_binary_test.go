package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/testutil"
)

const (
	binaryFirstBytes = 1 << 20
	binaryTotalBytes = 32 << 20
)

func binaryPayloadByte(i int) byte {
	return byte(i*131 + 17)
}

func writeBinaryPayload(w io.Writer, from, until int) error {
	buf := make([]byte, 32<<10)
	for from < until {
		n := until - from
		if n > len(buf) {
			n = len(buf)
		}
		for i := 0; i < n; i++ {
			buf[i] = binaryPayloadByte(from + i)
		}
		wrote := 0
		for wrote < n {
			nw, err := w.Write(buf[wrote:n])
			wrote += nw
			if err != nil {
				return err
			}
			if nw == 0 {
				return io.ErrShortWrite
			}
		}
		from += n
	}
	return nil
}

func hashBinaryPayload(n int) []byte {
	h := sha256.New()
	_ = writeBinaryPayload(h, 0, n)
	return h.Sum(nil)
}

type hashingObservedWriter struct {
	observedWriter
	sum hash.Hash
}

func (w *hashingObservedWriter) Write(p []byte) (int, error) {
	n, err := w.observedWriter.Write(p)
	_, _ = w.sum.Write(p[:n])
	return n, err
}

func binarySpec() CommandSpec {
	return CommandSpec{
		Group:    "Reports",
		Use:      "download",
		Method:   http.MethodGet,
		PathTpl:  "/r",
		Output:   OutputHints{ResponseMediaType: "application/pdf", Binary: true},
		Security: &SecurityHint{Public: true},
	}
}

func countRequests(handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler(w, r)
	}))
	return srv, &requests
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	testutil.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	testutil.Require(t, len(dirNames(t, dir)) == 0, "dir = %v", dirNames(t, dir))
}

func TestBuild_BinaryFileStreamsBeforeCompletion(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.pdf")
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	started := make(chan struct{})
	srv, requests := countRequests(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		if err := writeBinaryPayload(w, 0, binaryFirstBytes); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = writeBinaryPayload(w, binaryFirstBytes, binaryTotalBytes)
	})
	defer srv.Close()

	root := newExecutionRoot("table")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()
	select {
	case <-started:
	case err := <-errCh:
		t.Fatalf("command finished before the response blocked: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not pause the binary response")
	}

	deadline := time.Now().Add(2 * time.Second)
	sawPart := false
	for time.Now().Before(deadline) && !sawPart {
		for _, name := range dirNames(t, dir) {
			if name == "out.pdf" {
				t.Fatal("out.pdf exists before the response completed")
			}
			if strings.HasPrefix(name, ".out.pdf.") && strings.HasSuffix(name, ".part") {
				info, err := os.Stat(filepath.Join(dir, name))
				testutil.NoError(t, err)
				if info.Size() > 0 {
					sawPart = true
				}
			}
		}
		if !sawPart {
			time.Sleep(10 * time.Millisecond)
		}
	}
	testutil.Require(t, sawPart, "partial file was not written while the response was blocked")
	unblock()
	select {
	case err := <-errCh:
		testutil.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("command did not finish")
	}
	testutil.Require(t, requests.Load() == 1, "requests = %d", requests.Load())
	got, err := os.ReadFile(outPath)
	testutil.NoError(t, err)
	sum := sha256.Sum256(got)
	testutil.Require(t, bytes.Equal(sum[:], hashBinaryPayload(binaryTotalBytes)), "file hash mismatch")
	info, err := os.Stat(outPath)
	testutil.NoError(t, err)
	testutil.Require(t, info.Mode().Perm() == 0o600, "mode = %#o", info.Mode().Perm())
	testutil.Require(t, len(dirNames(t, dir)) == 1 && dirNames(t, dir)[0] == "out.pdf", "dir = %v", dirNames(t, dir))
}

func TestBuild_BinaryStdoutStreamsBeforeCompletion(t *testing.T) {
	isolateRuntime(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		if err := writeBinaryPayload(w, 0, binaryFirstBytes); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = writeBinaryPayload(w, binaryFirstBytes, binaryTotalBytes)
	}))
	defer srv.Close()

	out := &hashingObservedWriter{observedWriter: observedWriter{wrote: make(chan struct{})}, sum: sha256.New()}
	root := newExecutionRoot("table")
	root.SetOut(out)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", "-"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()
	select {
	case <-started:
	case err := <-errCh:
		t.Fatalf("command finished before the response blocked: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not pause the binary response")
	}
	select {
	case <-out.wrote:
	case <-time.After(2 * time.Second):
		unblock()
		t.Fatal("stdout received no bytes before the response completed")
	}
	unblock()
	select {
	case err := <-errCh:
		testutil.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("command did not finish")
	}
	testutil.Require(t, bytes.Equal(out.sum.Sum(nil), hashBinaryPayload(binaryTotalBytes)), "stdout hash mismatch")
}

func TestBuild_BinaryCancelLeavesNoFile(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.pdf")
	release := make(chan struct{})
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		if err := writeBinaryPayload(w, 0, binaryFirstBytes); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := newExecutionRoot("table")
	root.SetContext(ctx)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()
	select {
	case <-started:
	case err := <-errCh:
		t.Fatalf("command finished before the response blocked: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not pause the binary response")
	}
	cancel()
	select {
	case err := <-errCh:
		le := ClassifyError(err)
		testutil.Require(t, le.Code == CodeCanceled, "code = %s, err = %v", le.Code, err)
	case <-time.After(5 * time.Second):
		t.Fatal("command did not finish after cancel")
	}
	assertDirEmpty(t, dir)
}

func TestBuild_BinaryReadFailureLeavesNoFile(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.pdf")
	hijacked := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("0123456789"))
		hj, ok := w.(http.Hijacker)
		if !ok {
			hijacked <- os.ErrInvalid
			return
		}
		conn, _, err := hj.Hijack()
		hijacked <- err
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	root := newExecutionRoot("table")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})
	err := root.Execute()
	testutil.Require(t, err != nil, "expected a read failure")
	select {
	case hijackErr := <-hijacked:
		testutil.NoError(t, hijackErr)
	default:
		t.Fatal("handler did not hijack the response")
	}
	assertDirEmpty(t, dir)
}

func TestBuild_BinaryRefusesExistingPaths(t *testing.T) {
	isolateRuntime(t)
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) string
	}{
		{
			name: "file",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "out.pdf")
				testutil.NoError(t, os.WriteFile(path, []byte("keep"), 0o644))
				return path
			},
		},
		{
			name: "directory",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "out.pdf")
				testutil.NoError(t, os.Mkdir(path, 0o755))
				return path
			},
		},
		{
			name: "dangling symlink",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "out.pdf")
				testutil.NoError(t, os.Symlink(filepath.Join(dir, "missing"), path))
				return path
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tc.setup(t, dir)
			before := dirSnapshot(t, dir)
			srv, requests := countRequests(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("nope"))
			})
			defer srv.Close()
			root := newExecutionRoot("table")
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
			root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", path})
			err := root.Execute()
			le := ClassifyError(err)
			testutil.Require(t, le.Code == CodeUsage && le.ExitCode == ExitUsage, "error = %#v", le)
			testutil.Require(t, !bytes.Contains([]byte(le.Detail), []byte(filepath.Base(path))), "detail echoes path: %q", le.Detail)
			testutil.Require(t, requests.Load() == 0, "requests = %d", requests.Load())
			testutil.Require(t, dirSnapshot(t, dir) == before, "dir changed: %q", dirSnapshot(t, dir))
		})
	}
}

func dirSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	for _, name := range dirNames(t, dir) {
		info, err := os.Lstat(filepath.Join(dir, name))
		testutil.NoError(t, err)
		buf.WriteString(name)
		buf.WriteByte('\n')
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(filepath.Join(dir, name))
			testutil.NoError(t, err)
			buf.Write(data)
		}
	}
	return buf.String()
}

func TestBuild_BinaryMissingFlagBeforeHost(t *testing.T) {
	isolateRuntime(t)
	spec := binarySpec()
	spec.Security = nil
	srv, requests := countRequests(func(http.ResponseWriter, *http.Request) {})
	defer srv.Close()
	root := newExecutionRoot("table")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{spec})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download"})
	err := root.Execute()
	le := ClassifyError(err)
	testutil.Require(t, le.Code == CodeUsage && le.ExitCode == ExitUsage, "error = %#v", le)
	testutil.Require(t, strings.Contains(le.Detail, "output-file"), "detail = %q", le.Detail)
	testutil.Require(t, requests.Load() == 0, "requests = %d", requests.Load())
}

func TestBuild_BinaryUnexpectedMediaType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		media       string
		stdout      bool
	}{
		{name: "json file", contentType: "application/json", body: `{"message":"secret-body-token"}`, media: "application/json"},
		{name: "json stdout", contentType: "application/json; charset=utf-8", body: `{"message":"secret-body-token"}`, media: "application/json", stdout: true},
		{name: "html file", contentType: "text/html", body: "<html>secret-body-token</html>", media: "text/html"},
		{name: "problem json", contentType: "application/problem+json", body: `{"message":"secret-body-token"}`, media: "application/problem+json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRuntime(t)
			dir := t.TempDir()
			outPath := filepath.Join(dir, "out.pdf")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			var stdout bytes.Buffer
			root := newExecutionRoot("table")
			root.SetOut(&stdout)
			root.SetErr(io.Discard)
			mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
			args := []string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file"}
			if tc.stdout {
				args = append(args, "-")
			} else {
				args = append(args, outPath)
			}
			root.SetArgs(args)
			err := root.Execute()
			le := ClassifyError(err)
			testutil.Require(t, le.Code == CodeAPIError && le.ExitCode == ExitAPIError, "error = %#v", le)
			testutil.Require(t, strings.Contains(le.Detail, tc.media), "detail = %q", le.Detail)
			testutil.Require(t, !strings.Contains(le.Detail, "secret-body-token") && !strings.Contains(le.Detail, outPath) && !strings.Contains(le.Detail, "out.pdf"), "detail = %q", le.Detail)
			testutil.Require(t, !bytes.Contains(stdout.Bytes(), []byte("secret-body-token")), "stdout = %q", stdout.String())
			assertDirEmpty(t, dir)
		})
	}
}

func TestBuild_BinaryDeclaredTypeContentGuard(t *testing.T) {
	body := []byte(`{"ok":true}`)
	cases := []struct {
		name     string
		declared string
		keep     bool
	}{
		{name: "octet-stream keeps json", declared: "application/octet-stream", keep: true},
		{name: "wildcard keeps json", declared: "*/*", keep: true},
		{name: "pdf rejects json", declared: "application/pdf", keep: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRuntime(t)
			dir := t.TempDir()
			outPath := filepath.Join(dir, "out.bin")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			spec := binarySpec()
			spec.Output.ResponseMediaType = tc.declared
			root := newExecutionRoot("table")
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			mustBuild(t, root, "demo", []CommandSpec{spec})
			root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})
			err := root.Execute()
			if tc.keep {
				testutil.NoError(t, err)
				got, readErr := os.ReadFile(outPath)
				testutil.NoError(t, readErr)
				testutil.Require(t, bytes.Equal(got, body), "file = %q", got)
				return
			}
			le := ClassifyError(err)
			testutil.Require(t, le.Code == CodeAPIError && le.ExitCode == ExitAPIError, "error = %#v", le)
			testutil.Require(t, strings.Contains(le.Detail, "application/json"), "detail = %q", le.Detail)
			assertDirEmpty(t, dir)
		})
	}
}

func TestBuild_BinaryCommitFailureAfterRequest(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.pdf")
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		if _, err := w.Write([]byte("pdf-bytes")); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	root := newExecutionRoot("table")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})
	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()
	select {
	case <-started:
	case err := <-errCh:
		t.Fatalf("command finished before the response blocked: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not pause the binary response")
	}
	testutil.NoError(t, os.WriteFile(outPath, []byte("old"), 0o600))
	unblock()
	select {
	case err := <-errCh:
		le := ClassifyError(err)
		testutil.Require(t, le.Code == CodeGeneral && le.ExitCode == ExitGeneral, "error = %#v", le)
		testutil.Require(t, strings.Contains(le.Detail, "request completed") && strings.Contains(le.Detail, "not written"), "detail = %q", le.Detail)
		testutil.Require(t, !strings.Contains(le.Detail, outPath) && !strings.Contains(le.Detail, "out.pdf"), "detail = %q", le.Detail)
	case <-time.After(5 * time.Second):
		t.Fatal("command did not finish")
	}
	got, err := os.ReadFile(outPath)
	testutil.NoError(t, err)
	testutil.Require(t, string(got) == "old", "content = %q", got)
	names := dirNames(t, dir)
	testutil.Require(t, len(names) == 1 && names[0] == "out.pdf", "dir = %v", names)
}

func TestBuild_BinaryMissingDirNamesErrno(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "zz-absent-dir", "out.pdf")
	srv, requests := countRequests(func(http.ResponseWriter, *http.Request) {})
	defer srv.Close()
	root := newExecutionRoot("table")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})
	err := root.Execute()
	le := ClassifyError(err)
	testutil.Require(t, le.Code == CodeUsage && le.ExitCode == ExitUsage, "error = %#v", le)
	testutil.Require(t, strings.Contains(le.Detail, "no such file or directory"), "detail = %q", le.Detail)
	testutil.Require(t, strings.Contains(le.Detail, "output-file"), "detail = %q", le.Detail)
	testutil.Require(t, !strings.Contains(le.Detail, "zz-absent-dir") && !strings.Contains(le.Detail, outPath), "detail = %q", le.Detail)
	testutil.Require(t, requests.Load() == 0, "requests = %d", requests.Load())
	assertDirEmpty(t, dir)
}

func TestBuild_BinaryErrorRemovesPartialFile(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.pdf")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	defer srv.Close()
	root := newExecutionRoot("table")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--output-file", outPath})
	err := root.Execute()
	le := ClassifyError(err)
	testutil.Require(t, le.Code == CodeAPIError && le.Detail == "nope" && le.HTTP != nil && le.HTTP.Status == 404, "error = %#v", le)
	assertDirEmpty(t, dir)
}

func TestBuild_BinaryDryRunWithoutOutputFile(t *testing.T) {
	isolateRuntime(t)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	testutil.NoError(t, os.WriteFile(sentinel, []byte("stay"), 0o644))
	before := dirSnapshot(t, dir)
	srv, requests := countRequests(func(http.ResponseWriter, *http.Request) {})
	defer srv.Close()
	var out bytes.Buffer
	root := newExecutionRoot("table")
	root.SetOut(&out)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{binarySpec()})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "reports", "download", "--dry-run"})
	testutil.NoError(t, root.Execute())
	testutil.Require(t, requests.Load() == 0, "requests = %d", requests.Load())
	testutil.Require(t, dirSnapshot(t, dir) == before, "dir changed")
	testutil.Require(t, bytes.Contains(out.Bytes(), []byte(`"method"`)) && !bytes.Contains(out.Bytes(), []byte(`"binary"`)), "dry-run output = %s", out.String())
}

func TestBuild_BinaryCatalogFlag(t *testing.T) {
	root := newRootWithModuleGroup()
	spec := binarySpec()
	spec.Shortcuts = []CommandShortcut{{Use: "fetch-report"}}
	jsonSpec := CommandSpec{
		Group:    "Reports",
		Use:      "show",
		Method:   http.MethodGet,
		PathTpl:  "/reports/{id}",
		Output:   OutputHints{ResponseMediaType: "application/json"},
		Security: &SecurityHint{Public: true},
	}
	mustBuild(t, root, "demo", []CommandSpec{spec, jsonSpec})

	var download, show CatalogCommand
	for _, cmd := range BuildCatalog(root, CatalogOptions{}).Commands {
		switch cmd.Use {
		case "download":
			download = cmd
		case "show":
			show = cmd
		}
	}
	testutil.Require(t, download.Output.Binary != nil && download.Output.Binary.Flag == "output-file", "binary = %+v", download.Output.Binary)
	downloadCmd := commandByPath(root, "demo", "reports", "download")
	testutil.Require(t, downloadCmd != nil && downloadCmd.Flags().Lookup("output-file") != nil, "missing --output-file")
	shortcut := findChildCommand(root, "fetch-report")
	testutil.Require(t, shortcut != nil && shortcut.Flags().Lookup("output-file") != nil, "shortcut missing --output-file")
	testutil.Require(t, show.Output.Binary == nil, "json binary = %+v", show.Output.Binary)
	showCmd := commandByPath(root, "demo", "reports", "show")
	testutil.Require(t, showCmd != nil && showCmd.Flags().Lookup("output-file") == nil, "json command registered --output-file")
}

func TestBuild_BinaryFlagCollision(t *testing.T) {
	root := newRootWithModuleGroup()
	spec := binarySpec()
	spec.Params = []ParamSpec{{Name: "output-file", Flag: "output-file", In: InQuery, GoType: "string"}}
	mustBuild(t, root, "demo", []CommandSpec{spec})
	cmd := BuildCatalog(root, CatalogOptions{}).Commands[0]
	testutil.Require(t, cmd.Output.Binary != nil && cmd.Output.Binary.Flag == "lathe-output-file", "binary = %+v", cmd.Output.Binary)
	cobraCmd := commandByPath(root, cmd.Path...)
	testutil.Require(t, cobraCmd != nil && cobraCmd.Flags().Lookup("lathe-output-file") != nil, "missing renamed flag")
}

func TestBuild_BinaryPostHasNoWait(t *testing.T) {
	root := newRootWithModuleGroup()
	spec := binarySpec()
	spec.Method = http.MethodPost
	mustBuild(t, root, "demo", []CommandSpec{spec})
	cmd := commandByPath(root, "demo", "reports", "download")
	testutil.Require(t, cmd != nil && cmd.Flags().Lookup("wait") == nil && cmd.Flags().Lookup("lathe-wait") == nil, "binary POST registered --wait")
}

func TestBinaryFileCommitDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.pdf")
	sink, err := createBinaryFile(path)
	testutil.NoError(t, err)
	_, err = sink.tmp.Write([]byte("new-bytes"))
	testutil.NoError(t, err)
	testutil.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	err = sink.commit()
	testutil.Require(t, err != nil, "commit err = %v", err)
	got, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Require(t, string(got) == "old", "content = %q", got)
	testutil.Require(t, len(dirNames(t, dir)) == 1 && dirNames(t, dir)[0] == "out.pdf", "dir = %v", dirNames(t, dir))
}

func commandByPath(root *cobra.Command, path ...string) *cobra.Command {
	cur := root
	for _, name := range path {
		cur = findChildCommand(cur, name)
		if cur == nil {
			return nil
		}
	}
	return cur
}
