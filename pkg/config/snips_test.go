package config

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestSnipsPersistenceContract(t *testing.T) {
	m := &Manifest{CLI: CLIInfo{Name: "demo", ConfigDir: "demo", ConfigDirEnv: "DEMO_CONFIG_DIR", HostEnv: "DEMO_HOST"}}
	Bind(m)
	dir := t.TempDir()
	t.Setenv("DEMO_CONFIG_DIR", dir)

	empty, err := LoadSnips()
	testutil.NoError(t, err)
	testutil.Require(t, len(empty.Names()) == 0, "expected empty snips, got %v", empty.Names())

	err = MutateSnips(context.Background(), func(s *Snips) error {
		s.Set("zeta", SnipEntry{Command: "users list --limit 20", Description: "List users"})
		s.Set("alpha", SnipEntry{Command: "users get --id $1"})
		return nil
	})
	testutil.NoError(t, err)

	path := filepath.Join(dir, "snips.yml")
	info, err := os.Stat(path)
	testutil.NoError(t, err)
	if runtime.GOOS != "windows" {
		testutil.Require(t, info.Mode().Perm() == 0o600, "snips.yml perm = %o, want 0600", info.Mode().Perm())
	}

	loaded, err := LoadSnips()
	testutil.NoError(t, err)
	testutil.Require(t, slices.Equal(loaded.Names(), []string{"alpha", "zeta"}), "names = %v", loaded.Names())
	zeta, ok := loaded.Get("zeta")
	testutil.Require(t, ok && zeta.Command == "users list --limit 20" && zeta.Description == "List users", "zeta = %+v", zeta)

	err = os.WriteFile(path, []byte("quick: users list --limit 5\ndetailed:\n  command: users get --id $1\n  description: One user\n"), 0o600)
	testutil.NoError(t, err)
	handEdited, err := LoadSnips()
	testutil.NoError(t, err)
	quick, ok := handEdited.Get("quick")
	testutil.Require(t, ok && quick.Command == "users list --limit 5" && quick.Description == "", "quick = %+v", quick)

	err = MutateSnips(context.Background(), func(s *Snips) error {
		testutil.Require(t, s.Delete("quick"), "expected quick to be deleted")
		testutil.Require(t, !s.Delete("quick"), "expected second delete to return false")
		return nil
	})
	testutil.NoError(t, err)
	afterDelete, err := LoadSnips()
	testutil.NoError(t, err)
	testutil.Require(t, slices.Equal(afterDelete.Names(), []string{"detailed"}), "names after delete = %v", afterDelete.Names())

	for _, invalid := range []string{
		"bad:\n  command: \"\"\n",
		"bad:\n  command: users list\n  extra: nope\n",
		"- not-a-map\n",
	} {
		testutil.NoError(t, os.WriteFile(path, []byte(invalid), 0o600))
		_, err := LoadSnips()
		testutil.Require(t, err != nil, "expected error for invalid snips.yml:\n%s", invalid)
	}
}
