package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func bindTestManifest(t *testing.T, name, hostEnv string) {
	t.Helper()
	config.Bind(&config.Manifest{CLI: config.CLIInfo{
		Name:         name,
		ConfigDir:    name,
		ConfigDirEnv: strings.ToUpper(name) + "_CONFIG_DIR",
		HostEnv:      hostEnv,
	}})
}

func TestNewNotAuthenticatedError_WrapsSentinel(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")
	err := NewNotAuthenticatedError()
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatal("expected errors.Is to match ErrNotAuthenticated")
	}
	if !strings.Contains(err.Error(), "demo host") || !strings.Contains(err.Error(), "`demo auth login`") {
		t.Errorf("expected rendered message to use bound name; got %q", err.Error())
	}
}

func TestResolveHost_UsesBoundHostEnv(t *testing.T) {
	bindTestManifest(t, "myapp", "MYAPP_HOST")
	t.Setenv("MYAPP_HOST", "example.internal")
	t.Setenv("OTHER_HOST", "should-be-ignored")

	root := &cobra.Command{Use: "myapp"}
	root.PersistentFlags().String("hostname", "", "")

	got, err := ResolveHost(root)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if got != "example.internal" {
		t.Errorf("want example.internal, got %q", got)
	}
}
