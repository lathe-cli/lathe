package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func hostTestRoot(t *testing.T, hostnameDefault string) *cobra.Command {
	t.Helper()
	bindTestManifest(t, "demo", "DEMO_HOST")
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	t.Setenv("DEMO_HOST", "")
	root := &cobra.Command{Use: "demo"}
	root.SetContext(context.Background())
	root.PersistentFlags().String("hostname", hostnameDefault, "")
	root.PersistentFlags().Bool("insecure", false, "")
	return root
}

func saveTestHosts(t *testing.T, defaultHost string, names ...string) {
	t.Helper()
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	for _, name := range names {
		hosts.Set(name, config.HostEntry{AuthType: "bearer", OAuthToken: "tok"})
	}
	if defaultHost != "" {
		hosts.SetDefault(defaultHost)
	}
	if err := hosts.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestResolveHost_OrderMatrix(t *testing.T) {
	root := hostTestRoot(t, "")
	saveTestHosts(t, "staging.example.com", "prod.example.com", "staging.example.com")

	t.Run("flag over env persisted codegen", func(t *testing.T) {
		t.Setenv("DEMO_HOST", "env.example.com")
		if err := root.PersistentFlags().Set("hostname", "flag.example.com"); err != nil {
			t.Fatal(err)
		}
		res, err := resolveHost(root, "codegen.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if res.Hostname != "flag.example.com" || res.Source != HostSourceFlag {
			t.Fatalf("got %+v", res)
		}
	})
	t.Run("env over persisted codegen", func(t *testing.T) {
		if err := root.PersistentFlags().Set("hostname", ""); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEMO_HOST", "env.example.com")
		res, err := resolveHost(root, "codegen.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if res.Hostname != "env.example.com" || res.Source != HostSourceEnv {
			t.Fatalf("got %+v", res)
		}
	})
	t.Run("persisted over codegen", func(t *testing.T) {
		if err := root.PersistentFlags().Set("hostname", ""); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEMO_HOST", "")
		res, err := resolveHost(root, "codegen.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if res.Hostname != "staging.example.com" || res.Source != HostSourcePersisted {
			t.Fatalf("got %+v", res)
		}
	})
	t.Run("codegen over unique and multi-host error", func(t *testing.T) {
		hosts, err := config.LoadHosts()
		if err != nil {
			t.Fatal(err)
		}
		hosts.ClearDefault()
		if err := hosts.Save(); err != nil {
			t.Fatal(err)
		}
		res, err := resolveHost(root, "codegen.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if res.Hostname != "codegen.example.com" || res.Source != HostSourceCodegenDefault {
			t.Fatalf("got %+v", res)
		}
	})
}

func TestResolveHost_UniqueAndMultiHostError(t *testing.T) {
	root := hostTestRoot(t, "")
	saveTestHosts(t, "", "only.example.com")
	res, err := resolveHost(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Hostname != "only.example.com" || res.Source != HostSourceUnique {
		t.Fatalf("got %+v", res)
	}

	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	hosts.Set("other.example.com", config.HostEntry{AuthType: "bearer", OAuthToken: "tok"})
	if err := hosts.Save(); err != nil {
		t.Fatal(err)
	}
	_, err = resolveHost(root, "")
	if err == nil {
		t.Fatal("expected multi-host error")
	}
	if !strings.Contains(err.Error(), "host default set") || !strings.Contains(err.Error(), "DEMO_HOST") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveHost_StalePersistedDefaultFallsThrough(t *testing.T) {
	root := hostTestRoot(t, "")
	saveTestHosts(t, "gone.example.com", "prod.example.com")

	stderr := captureStderr(t)
	res, err := resolveHost(root, "codegen.example.com")
	out := readStderr(t, stderr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Hostname != "codegen.example.com" || res.Source != HostSourceCodegenDefault {
		t.Fatalf("stale default must fall through, got %+v", res)
	}
	if !strings.Contains(out, `warning: persisted default host "gone.example.com" is not configured; clearing it`) {
		t.Fatalf("missing stale warning:\n%s", out)
	}
	reloaded, err := config.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Default() != "" {
		t.Fatalf("stale default not cleared: %q", reloaded.Default())
	}
}

func TestResolveHost_StalePersistedDefaultDoesNotPickSubstitute(t *testing.T) {
	root := hostTestRoot(t, "")
	saveTestHosts(t, "gone.example.com", "a.example.com", "b.example.com")
	stderr := captureStderr(t)
	res, err := resolveHost(root, "")
	_ = readStderr(t, stderr)
	if err == nil {
		t.Fatalf("expected multi-host error, got %+v", res)
	}
	if res.Hostname != "" {
		t.Fatalf("must not pick a substitute host, got %q", res.Hostname)
	}
	if !strings.Contains(err.Error(), "host default set") {
		t.Fatalf("error = %v", err)
	}
}

func TestNoticeImplicitHost_TriggerConditions(t *testing.T) {
	var buf bytes.Buffer
	noticeImplicitHost(&buf, HostResolution{Hostname: "staging.example.com", Source: HostSourcePersisted, Configured: 2})
	if !strings.Contains(buf.String(), "host: staging.example.com (persisted default)") {
		t.Fatalf("persisted notice = %q", buf.String())
	}

	buf.Reset()
	noticeImplicitHost(&buf, HostResolution{Hostname: "codegen.example.com", Source: HostSourceCodegenDefault, Configured: 2})
	if !strings.Contains(buf.String(), "host: codegen.example.com (codegen default)") {
		t.Fatalf("codegen notice = %q", buf.String())
	}

	buf.Reset()
	noticeImplicitHost(&buf, HostResolution{Hostname: "only.example.com", Source: HostSourceUnique, Configured: 1})
	if buf.Len() != 0 {
		t.Fatalf("unique single-host must be silent, got %q", buf.String())
	}

	buf.Reset()
	noticeImplicitHost(&buf, HostResolution{Hostname: "flag.example.com", Source: HostSourceFlag, Configured: 2})
	if buf.Len() != 0 {
		t.Fatalf("explicit flag must be silent, got %q", buf.String())
	}

	buf.Reset()
	noticeImplicitHost(&buf, HostResolution{Hostname: "env.example.com", Source: HostSourceEnv, Configured: 2})
	if buf.Len() != 0 {
		t.Fatalf("explicit env must be silent, got %q", buf.String())
	}
}

func TestWriteDryRun_IncludesHostProvenance(t *testing.T) {
	var buf bytes.Buffer
	if err := writeDryRun(DryRunRequest{
		Method:     "GET",
		URL:        "https://example.com/x",
		Hostname:   "example.com",
		HostSource: HostSourcePersisted,
	}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"hostname": "example.com"`) || !strings.Contains(buf.String(), `"host_source": "persisted"`) {
		t.Fatalf("dry-run JSON missing provenance:\n%s", buf.String())
	}
}

func TestResolveHost_NothingPersistedMatchesPriorErrorPlusHostDefaultSet(t *testing.T) {
	root := hostTestRoot(t, "")
	saveTestHosts(t, "", "a.example.com", "b.example.com")
	_, err := resolveHost(root, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "multiple hosts configured") || !strings.Contains(err.Error(), "host default set") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveHost_UsesBoundHostEnvSource(t *testing.T) {
	bindTestManifest(t, "myapp", "MYAPP_HOST")
	t.Setenv("MYAPP_HOST", "example.internal")
	root := &cobra.Command{Use: "myapp"}
	root.PersistentFlags().String("hostname", "", "")
	res, err := resolveHost(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Hostname != "example.internal" || res.Source != HostSourceEnv {
		t.Fatalf("got %+v", res)
	}
}
