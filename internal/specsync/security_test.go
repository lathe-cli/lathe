package specsync

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestSafeJoinRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(outside, "api.json"), []byte("synthetic-marker"), 0o644))
	testutil.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))
	testutil.NoError(t, os.Symlink(filepath.Join(outside, "api.json"), filepath.Join(root, "api.json")))
	for _, rel := range []string{"linked/api.json", "linked/missing/api.json", "api.json"} {
		if _, err := safeJoin(root, rel); err == nil {
			t.Errorf("safeJoin accepted %s", rel)
		}
	}
}

func TestCopyFileRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	link := filepath.Join(dir, "link")
	testutil.NoError(t, os.WriteFile(original, []byte("synthetic-marker"), 0o644))
	testutil.NoError(t, os.Symlink(original, link))
	if err := copyFile(link, filepath.Join(dir, "copy")); err == nil {
		t.Error("copied symlink source")
	}
	if err := copyFile(original, link); err == nil {
		t.Error("wrote symlink destination")
	}
}

func TestCopyProtoTreeRejectsSymlinks(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			file := filepath.Join(outside, "secret.proto")
			testutil.NoError(t, os.WriteFile(file, []byte("synthetic-marker"), 0o644))
			target := file
			if directory {
				target = outside
			}
			linkName := "linked.proto"
			if directory {
				linkName = "linked"
			}
			testutil.NoError(t, os.Symlink(target, filepath.Join(root, linkName)))
			if err := copyProtoTree(root, t.TempDir()); err == nil {
				t.Fatal("staged a symlink")
			}
		})
	}
}

func TestGitSeparatesRepositoryArgument(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in clone|ls-remote) ;; *) exit 1;; esac
for arg in "$@"; do
 [ "$arg" = "--" ] && exit 0
done
printf missing-separator > "$LATHE_GIT_ARGUMENT_FAILURE"
exit 1
`
	testutil.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755))
	failure := filepath.Join(t.TempDir(), "failure")
	t.Setenv("LATHE_GIT_ARGUMENT_FAILURE", failure)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, _ = ensureRepo(filepath.Join(t.TempDir(), "checkout"), "--upload-pack=synthetic", "v1.0.0")
	if _, err := os.Stat(failure); err == nil {
		t.Error("git clone did not separate repository argument")
	}
	_ = os.Remove(failure)
	_, _ = remoteRefSHA("--upload-pack=synthetic", "v1.0.0")
	if _, err := os.Stat(failure); err == nil {
		t.Error("git ls-remote did not separate repository argument")
	}
}

func TestGitErrorsDoNotDiscloseCredentials(t *testing.T) {
	bin := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s\\n' \"$LATHE_TEST_REPO_URL\" >&2\nexit 1\n"), 0o755))
	repo := "https://synthetic-user:synthetic-password@example.com/repo.git?token=synthetic-token"
	t.Setenv("LATHE_TEST_REPO_URL", repo)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := os.CreateTemp(t.TempDir(), "stderr")
	testutil.NoError(t, err)
	previous := os.Stderr
	os.Stderr = output
	defer func() { os.Stderr = previous; _ = output.Close() }()
	_, cloneErr := ensureRepo(filepath.Join(t.TempDir(), "checkout"), repo, "v1.0.0")
	_, remoteErr := remoteRefSHA(repo, "v1.0.0")
	os.Stderr = previous
	_, err = output.Seek(0, 0)
	testutil.NoError(t, err)
	data, err := io.ReadAll(output)
	testutil.NoError(t, err)
	diagnostic := string(data) + cloneErr.Error() + remoteErr.Error() + dependencyName(sourceconfig.ProtoDependency{Kind: sourceconfig.ProtoDependencyGit, RepoURL: repo, PinnedTag: "v1.0.0"})
	for _, marker := range []string{"synthetic-user", "synthetic-password", "synthetic-token"} {
		if strings.Contains(diagnostic, marker) {
			t.Errorf("diagnostic disclosed %s", marker)
		}
	}
}

func TestSyncAcceptsUppercaseSHAWithCachedCheckout(t *testing.T) {
	testutil.IsolateGitConfig(t)
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		testutil.Require(t, err == nil, "git fixture failed: %v", err)
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	testutil.NoError(t, os.WriteFile(filepath.Join(repo, "api.json"), []byte("{}"), 0o644))
	run("add", ".")
	run("-c", "user.name=Lathe Test", "-c", "user.email=lathe@example.com", "commit", "--quiet", "-m", "fixture")
	sha := strings.ToUpper(run("rev-parse", "HEAD"))
	cfg := &sourceconfig.Config{Sources: map[string]*sourceconfig.Source{"demo": {Name: "demo", RepoURL: repo, PinnedTag: sha, Backend: sourceconfig.BackendSwagger, Swagger: &sourceconfig.SwaggerConfig{Files: []string{"api.json"}}}}}
	cache := t.TempDir()
	testutil.NoError(t, Sync(cfg, Options{CacheRoot: cache}))
	testutil.NoError(t, Sync(cfg, Options{CacheRoot: cache}))
}

func TestLoadStateDoesNotDiscloseContent(t *testing.T) {
	dir := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, StateFile), []byte("canary"), 0o644))
	_, err := LoadState(dir)
	testutil.Require(t, err != nil, "invalid state accepted")
	if strings.Contains(err.Error(), "canary") {
		t.Fatal("state parse error disclosed content")
	}
}
