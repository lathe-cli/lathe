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
	writeSpec(2)
	runGit("tag", "-f", "v1.0.0")

	err := Sync(cfg, Options{CacheRoot: cache})
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
