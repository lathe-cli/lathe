package lathe

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestRunGitHubUpdateUsesReportedVersion(t *testing.T) {
	restoreVersionInfo(t)
	Version = "dev"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/demo/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v0.1.2"})
	}))
	defer srv.Close()

	oldBaseURL := updateGitHubAPIBaseURL
	oldClient := updateHTTPClient
	oldVersionInfo := updateVersionInfo
	t.Cleanup(func() {
		updateGitHubAPIBaseURL = oldBaseURL
		updateHTTPClient = oldClient
		updateVersionInfo = oldVersionInfo
	})
	updateGitHubAPIBaseURL = srv.URL
	updateHTTPClient = srv.Client()
	updateVersionInfo = func() (string, string, string) {
		return "v0.1.3-0.20260830061345-0837499497e7", "0837499", "2026-08-30T06:13:45Z"
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	m := &config.Manifest{
		CLI: config.CLIInfo{Name: "demo"},
		Update: config.UpdateInfo{GitHub: &config.GitHubUpdate{
			Owner: "acme",
			Repo:  "demo",
		}},
	}

	testutil.NoError(t, runGitHubUpdate(cmd, m, false))
	if got := out.String(); !strings.Contains(got, "demo is newer than latest release (v0.1.3-0.20260830061345-0837499497e7 > v0.1.2)") {
		t.Fatalf("output = %q", got)
	}
}

func TestUpdateVersionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.0.0-alpha.10", "1.0.0-alpha.2", 1},
		{"1.0.0+abc", "1.0.0+xyz", 0},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-99999999999999999999999", "1.0.0-9999999999999999999999", 1},
	} {
		got, err := compareVersions(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("compare(%q,%q)=%d,%v want %d", tc.a, tc.b, got, err, tc.want)
		}
	}
	for _, v := range []string{"1.0.0-01", "1.0.0-", "1.0.0+a..b", "-1.0.0", "01.0.0"} {
		if _, err := compareVersions(v, "1.0.0"); err == nil {
			t.Errorf("accepted invalid version %q", v)
		}
	}
}

func TestUpdateRejectsUnsafeRequests(t *testing.T) {
	oldBase, oldClient := updateGitHubAPIBaseURL, updateHTTPClient
	t.Cleanup(func() { updateGitHubAPIBaseURL = oldBase; updateHTTPClient = oldClient })
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.WriteString(w, `{"tag_name":"v1.0.0"}`)
	}))
	defer srv.Close()
	updateGitHubAPIBaseURL = srv.URL
	updateHTTPClient = srv.Client()
	for _, part := range []string{"", ".", "..", "a/b", `a\b`} {
		for _, gh := range []config.GitHubUpdate{{Owner: part, Repo: "demo"}, {Owner: "acme", Repo: part}} {
			if _, err := fetchLatestGitHubRelease(context.Background(), gh); err == nil {
				t.Errorf("accepted invalid owner/repo %+v", gh)
			}
		}
	}
	if hits != 0 {
		t.Errorf("invalid owner/repo made %d requests", hits)
	}
	updateHTTPClient = oldClient
	if p, err := downloadReleaseAsset(context.Background(), githubAsset{Name: "demo", BrowserDownloadURL: srv.URL}, ""); err == nil {
		_ = os.Remove(p)
		t.Error("default client accepted loopback HTTP")
	}
	transport := updateRoundTripper(func(r *http.Request) (*http.Response, error) {
		t.Error("unsafe request reached transport")
		return nil, fmt.Errorf("blocked fixture")
	})
	updateHTTPClient = &http.Client{Transport: transport}
	updateGitHubAPIBaseURL = "http://github.example"
	if _, err := fetchLatestGitHubRelease(context.Background(), config.GitHubUpdate{Owner: "acme", Repo: "demo"}); err == nil {
		t.Error("accepted HTTP API")
	}
	if p, err := downloadReleaseAsset(context.Background(), githubAsset{Name: "demo", BrowserDownloadURL: "http://assets.example/demo"}, ""); err == nil {
		_ = os.Remove(p)
		t.Error("accepted HTTP asset")
	}
}

type updateRoundTripper func(*http.Request) (*http.Response, error)

func (f updateRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateRedirectBoundary(t *testing.T) {
	old := updateHTTPClient
	t.Cleanup(func() { updateHTTPClient = old })
	hits := 0
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; _, _ = io.WriteString(w, "payload") }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, http.StatusFound) }))
	defer src.Close()
	updateHTTPClient = src.Client()
	p, err := downloadReleaseAsset(context.Background(), githubAsset{Name: "demo", BrowserDownloadURL: src.URL}, fmt.Sprintf("%x", sha256.Sum256([]byte("payload"))))
	if p != "" {
		_ = os.Remove(p)
	}
	if err == nil || hits != 0 {
		t.Errorf("cross-origin redirect: err=%v destination hits=%d", err, hits)
	}
	hits = 0
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, http.StatusFound) }))
	defer tls.Close()
	updateHTTPClient = tls.Client()
	if p, err := downloadReleaseAsset(context.Background(), githubAsset{Name: "demo", BrowserDownloadURL: tls.URL}, ""); err == nil {
		_ = os.Remove(p)
		t.Error("accepted HTTPS downgrade")
	}
	if hits != 0 {
		t.Errorf("downgrade reached destination %d times", hits)
	}
}

func TestUpdateReadLimits(t *testing.T) {
	oldBase, oldClient := updateGitHubAPIBaseURL, updateHTTPClient
	t.Cleanup(func() { updateGitHubAPIBaseURL = oldBase; updateHTTPClient = oldClient })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tag_name":"v1.0.0","padding":"`+strings.Repeat("x", 5<<20)+`"}`)
	}))
	defer srv.Close()
	updateGitHubAPIBaseURL = srv.URL
	updateHTTPClient = srv.Client()
	if _, err := fetchLatestGitHubRelease(context.Background(), config.GitHubUpdate{Owner: "acme", Repo: "demo"}); err == nil {
		t.Error("accepted oversized release JSON")
	}
	p, err := writeUpdatePayload(io.LimitReader(updateZeroReader{}, (128<<20)+1), 0o755)
	if p != "" {
		_ = os.Remove(p)
	}
	if err == nil {
		t.Error("accepted oversized binary")
	}
	archiveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, io.LimitReader(updateZeroReader{}, (256<<20)+1))
	}))
	defer archiveSrv.Close()
	updateHTTPClient = archiveSrv.Client()
	p, err = downloadReleaseAsset(context.Background(), githubAsset{Name: "demo", BrowserDownloadURL: archiveSrv.URL}, "")
	if p != "" {
		_ = os.Remove(p)
	}
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Errorf("archive bound not enforced: %v", err)
	}
}

type updateZeroReader struct{}

func (updateZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestUpdateArchiveRegularPayload(t *testing.T) {
	for _, kind := range []string{"zip", "tar.gz"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "demo."+kind)
			f, err := os.Create(path)
			testutil.NoError(t, err)
			if kind == "zip" {
				zw := zip.NewWriter(f)
				h := &zip.FileHeader{Name: "demo"}
				h.SetMode(os.ModeSymlink | 0o777)
				w, e := zw.CreateHeader(h)
				testutil.NoError(t, e)
				_, e = w.Write([]byte("symlink-target"))
				testutil.NoError(t, e)
				h = &zip.FileHeader{Name: "bin/demo"}
				h.SetMode(0o755)
				w, e = zw.CreateHeader(h)
				testutil.NoError(t, e)
				_, e = w.Write([]byte("binary"))
				testutil.NoError(t, e)
				testutil.NoError(t, zw.Close())
			} else {
				gz := gzip.NewWriter(f)
				tw := tar.NewWriter(gz)
				testutil.NoError(t, tw.WriteHeader(&tar.Header{Name: "demo", Typeflag: tar.TypeSymlink, Linkname: "target", Mode: 0o777}))
				testutil.NoError(t, tw.WriteHeader(&tar.Header{Name: "bin/demo", Typeflag: tar.TypeReg, Size: 6, Mode: 0o755}))
				_, err = tw.Write([]byte("binary"))
				testutil.NoError(t, err)
				testutil.NoError(t, tw.Close())
				testutil.NoError(t, gz.Close())
			}
			testutil.NoError(t, f.Close())
			archive, err := os.ReadFile(path)
			testutil.NoError(t, err)
			oldBase, oldClient, oldVersion, oldInstall := updateGitHubAPIBaseURL, updateHTTPClient, updateVersionInfo, installUpdate
			defer func() {
				updateGitHubAPIBaseURL = oldBase
				updateHTTPClient = oldClient
				updateVersionInfo = oldVersion
				installUpdate = oldInstall
			}()
			var serverURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/latest") {
					_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0", Assets: []githubAsset{{Name: "demo." + kind, BrowserDownloadURL: serverURL + "/asset", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(archive))}}})
					return
				}
				_, _ = w.Write(archive)
			}))
			defer srv.Close()
			serverURL = srv.URL
			updateGitHubAPIBaseURL = srv.URL
			updateHTTPClient = srv.Client()
			updateVersionInfo = func() (string, string, string) { return "0.9.0", "", "" }
			installed := false
			installUpdate = func(payload string) error {
				data, e := os.ReadFile(payload)
				if e != nil {
					return e
				}
				if string(data) != "binary" {
					return fmt.Errorf("selected non-regular payload %q", data)
				}
				installed = true
				return nil
			}
			m := &config.Manifest{CLI: config.CLIInfo{Name: "demo"}, Update: config.UpdateInfo{GitHub: &config.GitHubUpdate{Owner: "acme", Repo: "demo", Asset: "demo." + kind}}}
			cmd := NewApp(m)
			cmd.SetContext(context.Background())
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"update", "--yes"})
			testutil.NoError(t, cmd.Execute())
			if !installed {
				t.Error("update did not install payload")
			}

			out, err := updatePayloadPath(path, "demo."+kind, "demo")
			testutil.NoError(t, err)
			defer func() { _ = os.Remove(out) }()
			b, err := os.ReadFile(out)
			testutil.NoError(t, err)
			if string(b) != "binary" {
				t.Fatalf("payload=%q", b)
			}
		})
	}
}

func TestUpdateWindowsReplacementRollback(t *testing.T) {
	for _, failInstall := range []bool{false, true} {
		t.Run(fmt.Sprint(failInstall), func(t *testing.T) {
			dir := t.TempDir()
			src, target := filepath.Join(dir, "new"), filepath.Join(dir, "demo.exe")
			testutil.NoError(t, os.WriteFile(src, []byte("new"), 0o755))
			testutil.NoError(t, os.WriteFile(target, []byte("old"), 0o755))
			moved := false
			rename := func(from, to string) error {
				if to == target && !moved {
					return fmt.Errorf("Windows cannot overwrite a running executable")
				}
				if from == target {
					moved = true
				}
				if failInstall && from == src {
					return fmt.Errorf("install failed")
				}
				return os.Rename(from, to)
			}
			err := replaceUpdateFile(src, target, true, rename)
			if failInstall {
				if err == nil {
					t.Fatal("expected install failure")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := "new"
			if failInstall {
				want = "old"
			}
			data, e := os.ReadFile(target)
			testutil.NoError(t, e)
			if string(data) != want {
				t.Errorf("target=%q want %s", data, want)
			}
			if !moved {
				t.Error("did not rename running executable aside")
			}
		})
	}
}
