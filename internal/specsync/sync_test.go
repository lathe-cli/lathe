package specsync

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func TestSyncRejectsMovedTagWithExistingCheckout(t *testing.T) {
	upstream := filepath.Join(t.TempDir(), "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = upstream
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "--quiet")
	runGit("branch", "-m", "v1.0.0")
	writeSpec := func(version int) {
		t.Helper()
		body := []byte(fmt.Sprintf("{\"version\":%d}\n", version))
		if err := os.WriteFile(filepath.Join(upstream, "swagger.json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", "swagger.json")
		runGit("-c", "user.name=Lathe Test", "-c", "user.email=lathe@example.com", "commit", "--quiet", "-m", "fixture")
	}
	writeSpec(1)
	runGit("tag", "v1.0.0")
	writeSpec(2)

	cfg := &sourceconfig.Config{Sources: map[string]*sourceconfig.Source{
		"demo": {
			Name:      "demo",
			RepoURL:   upstream,
			PinnedTag: "v1.0.0",
			Backend:   sourceconfig.BackendSwagger,
			Swagger:   &sourceconfig.SwaggerConfig{Files: []string{"swagger.json"}},
		},
	}}
	cache := t.TempDir()
	if err := Sync(cfg, Options{CacheRoot: cache}); err != nil {
		t.Fatal(err)
	}
	synced, err := os.ReadFile(filepath.Join(cache, SyncSubdir, "demo", "swagger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(synced) != "{\"version\":1}\n" {
		t.Fatalf("synced spec = %q, want configured tag contents", synced)
	}
	runGit("tag", "-f", "v1.0.0")

	err = Sync(cfg, Options{CacheRoot: cache})
	if err == nil {
		t.Fatal("Sync accepted a cached checkout after its configured tag moved upstream")
	}
	if !strings.Contains(err.Error(), "changed upstream") {
		t.Fatalf("Sync error = %v, want moved-ref diagnostic", err)
	}
}

func TestSyncConcurrentProcessesShareCheckout(t *testing.T) {
	if name := os.Getenv("LATHE_SPECSYNC_CHILD"); name != "" {
		cfg := &sourceconfig.Config{Sources: map[string]*sourceconfig.Source{
			name: {
				Name:      name,
				RepoURL:   os.Getenv("LATHE_SPECSYNC_REPO"),
				PinnedTag: "v1.0.0",
				Backend:   sourceconfig.BackendSwagger,
				Swagger:   &sourceconfig.SwaggerConfig{Files: []string{"swagger.json"}},
			},
		}}
		if err := Sync(cfg, Options{CacheRoot: os.Getenv("LATHE_SPECSYNC_CACHE")}); err != nil {
			t.Fatal(err)
		}
		return
	}

	upstream := filepath.Join(t.TempDir(), "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = upstream
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "--quiet")
	if err := os.WriteFile(filepath.Join(upstream, "swagger.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "swagger.json")
	runGit("-c", "user.name=Lathe Test", "-c", "user.email=lathe@example.com", "commit", "--quiet", "-m", "fixture")
	runGit("tag", "v1.0.0")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapperDir := t.TempDir()
	barrierDir := t.TempDir()
	wrapper := filepath.Join(wrapperDir, "git")
	script := `#!/bin/sh
if [ "$1" = "clone" ]; then
  : > "$LATHE_SPECSYNC_BARRIER/ready-$$"
  attempts=0
  while :; do
    ready=$(find "$LATHE_SPECSYNC_BARRIER" -name 'ready-*' -type f | wc -l | tr -d ' ')
    [ "$ready" -ge 2 ] && break
    attempts=$((attempts + 1))
    [ "$attempts" -ge 500 ] && exit 99
    sleep 0.01
  done
fi
exec "$LATHE_SPECSYNC_REAL_GIT" "$@"
`
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cache := t.TempDir()
	command := func(name string, output *bytes.Buffer) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSyncConcurrentProcessesShareCheckout$", "-test.count=1")
		cmd.Env = append(os.Environ(),
			"LATHE_SPECSYNC_CHILD="+name,
			"LATHE_SPECSYNC_REPO="+upstream,
			"LATHE_SPECSYNC_CACHE="+cache,
			"LATHE_SPECSYNC_BARRIER="+barrierDir,
			"LATHE_SPECSYNC_REAL_GIT="+realGit,
			"PATH="+wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		cmd.Stdout = output
		cmd.Stderr = output
		return cmd
	}
	var outputA, outputB bytes.Buffer
	cmdA := command("qdrant-a", &outputA)
	cmdB := command("qdrant-b", &outputB)
	if err := cmdA.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmdB.Start(); err != nil {
		_ = cmdA.Process.Kill()
		_ = cmdA.Wait()
		t.Fatal(err)
	}
	errA := cmdA.Wait()
	errB := cmdB.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("concurrent sync failed: a=%v\n%s\nb=%v\n%s", errA, outputA.Bytes(), errB, outputB.Bytes())
	}
	checkouts, err := os.ReadDir(filepath.Join(cache, WorkSubdir))
	if err != nil {
		t.Fatal(err)
	}
	if len(checkouts) != 1 {
		t.Fatalf("checkout count = %d, want 1", len(checkouts))
	}
}

func TestSyncProtoStagesGitDependency(t *testing.T) {
	dependency := filepath.Join(t.TempDir(), "dependency")
	if err := os.MkdirAll(filepath.Join(dependency, "types"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "types", "types.proto"), []byte("syntax = \"proto3\"; package types; message Value {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dependency
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "--quiet")
	runGit("add", ".")
	runGit("-c", "user.name=Lathe Test", "-c", "user.email=lathe@example.com", "commit", "--quiet", "-m", "fixture")
	sha := runGit("rev-parse", "HEAD")

	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	service := "syntax = \"proto3\"; package api; import \"example.com/dependency/types/types.proto\"; service Demo {}\n"
	if err := os.WriteFile(filepath.Join(source, "api", "service.proto"), []byte(service), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	protoc := filepath.Join(binDir, "protoc")
	script := `#!/bin/sh
set -eu
[ -f example.com/dependency/types/types.proto ]
out=
for arg in "$@"; do
  case "$arg" in --descriptor_set_out=*) out=${arg#*=};; esac
done
[ -n "$out" ]
: > "$out"
`
	if err := os.WriteFile(protoc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &sourceconfig.Config{Sources: map[string]*sourceconfig.Source{
		"demo": {
			Name:      "demo",
			LocalPath: source,
			Backend:   sourceconfig.BackendProto,
			Proto: &sourceconfig.ProtoConfig{
				Staging: []sourceconfig.StagingEntry{{From: ".", To: "."}},
				Entries: []string{"api/service.proto"},
				Dependencies: []sourceconfig.ProtoDependency{{
					Kind:      sourceconfig.ProtoDependencyGit,
					RepoURL:   dependency,
					PinnedTag: sha,
					Staging:   []sourceconfig.StagingEntry{{From: ".", To: "example.com/dependency"}},
				}},
			},
		},
	}}
	if err := Sync(cfg, Options{CacheRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestCopyProtoTreeRejectsConflictingStaging(t *testing.T) {
	first, second, dst := t.TempDir(), t.TempDir(), t.TempDir()
	stage := func(dir, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "google", "api"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "google", "api", "annotations.proto"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stage(first, "syntax = \"proto3\";\n")
	stage(second, "syntax = \"proto3\";\n")
	if err := copyProtoTree(first, dst); err != nil {
		t.Fatal(err)
	}
	if err := copyProtoTree(second, dst); err != nil {
		t.Fatalf("identical staged content must not collide: %v", err)
	}
	stage(second, "syntax = \"proto3\";\npackage other;\n")
	if err := copyProtoTree(second, dst); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("conflicting staged content error = %v", err)
	}
}

func TestMaterializeProtoDependencyGoModuleVerifiesSum(t *testing.T) {
	moduleDir := t.TempDir()
	binDir := t.TempDir()
	goBin := filepath.Join(binDir, "go")
	script := `#!/bin/sh
set -eu
printf '{"Dir":"%s","Sum":"h1:fixture"}\n' "$LATHE_FAKE_GO_MODULE"
`
	if err := os.WriteFile(goBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LATHE_FAKE_GO_MODULE", moduleDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dep := sourceconfig.ProtoDependency{
		Kind: sourceconfig.ProtoDependencyGoModule, Module: "example.com/dependency", Version: "v1.2.3", Sum: "h1:fixture",
	}
	got, err := materializeProtoDependency(dep, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != moduleDir {
		t.Fatalf("module dir = %q, want %q", got, moduleDir)
	}
	dep.Sum = "h1:wrong"
	if _, err := materializeProtoDependency(dep, t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum mismatch error = %v", err)
	}
}

func TestMaterializeProtoDependencyBufCachesExport(t *testing.T) {
	binDir := t.TempDir()
	bufBin := filepath.Join(binDir, "buf")
	logPath := filepath.Join(t.TempDir(), "calls")
	script := `#!/bin/sh
set -eu
if [ "$1" = "dep" ]; then
  dir=$3
  printf '# Generated by buf. DO NOT EDIT.\nversion: v2\ndeps:\n  - name: buf.build/googleapis/googleapis\n    commit: 004180b77378443887d3b55cabc00384\n    digest: b5:fixture\n' > "$dir/buf.lock"
  exit 0
fi
out=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out=$2; shift 2; continue; fi
  shift
done
[ -n "$out" ]
mkdir -p "$out/google/api"
printf 'syntax = "proto3";\n' > "$out/google/api/annotations.proto"
printf 'x\n' >> "$LATHE_FAKE_BUF_LOG"
`
	if err := os.WriteFile(bufBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LATHE_FAKE_BUF_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dep := sourceconfig.ProtoDependency{
		Kind: sourceconfig.ProtoDependencyBuf, Module: "buf.build/googleapis/googleapis",
		Commit: "004180b77378443887d3b55cabc00384", Digest: "b5:fixture",
	}
	workRoot := t.TempDir()
	first, err := materializeProtoDependency(dep, workRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializeProtoDependency(dep, workRoot)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("cached roots differ: %q != %q", first, second)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "x\n" {
		t.Fatalf("buf calls = %q, want one export", calls)
	}
	dep.Digest = "b5:wrong"
	if _, err := materializeProtoDependency(dep, t.TempDir()); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}
