package runtime

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func hostResolutionRoot(t *testing.T) *cobra.Command {
	t.Helper()
	bindTestManifest(t, "demo", "DEMO_HOST")
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	t.Setenv("DEMO_HOST", "")
	root := &cobra.Command{Use: "demo"}
	root.SetContext(context.Background())
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().Bool("insecure", false, "")
	return root
}

func saveHosts(t *testing.T, defaultHost string, names ...string) {
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

func TestResolveHostOrder(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		env        string
		persisted  string
		hosts      []string
		codegen    string
		wantHost   string
		wantSource string
		wantErr    string
	}{
		{"flag beats env persisted codegen", "flag.example.com", "env.example.com", "staging.example.com", []string{"prod.example.com", "staging.example.com"}, "codegen.example.com", "flag.example.com", hostSourceFlag, ""},
		{"env beats persisted codegen", "", "env.example.com", "staging.example.com", []string{"prod.example.com", "staging.example.com"}, "codegen.example.com", "env.example.com", hostSourceEnv, ""},
		{"persisted beats codegen", "", "", "staging.example.com", []string{"prod.example.com", "staging.example.com"}, "codegen.example.com", "staging.example.com", hostSourcePersisted, ""},
		{"codegen beats unique and multi-host error", "", "", "", []string{"a.example.com", "b.example.com"}, "codegen.example.com", "codegen.example.com", hostSourceCodegenDefault, ""},
		{"unique host", "", "", "", []string{"only.example.com"}, "", "only.example.com", hostSourceUnique, ""},
		{"multi-host error points at host default set", "", "", "", []string{"a.example.com", "b.example.com"}, "", "", "", "multiple hosts configured"},
		{"no hosts is not authenticated", "", "", "", nil, "", "", "", "not logged in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := hostResolutionRoot(t)
			saveHosts(t, tt.persisted, tt.hosts...)
			if tt.flag != "" {
				if err := root.PersistentFlags().Set("hostname", tt.flag); err != nil {
					t.Fatal(err)
				}
			}
			if tt.env != "" {
				t.Setenv("DEMO_HOST", tt.env)
			}
			res, err := resolveHost(root, tt.codegen)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Hostname != tt.wantHost || res.Source != tt.wantSource {
				t.Fatalf("got %+v, want %q via %q", res, tt.wantHost, tt.wantSource)
			}
		})
	}
}

func TestResolveHostMultiHostErrorIsActionable(t *testing.T) {
	root := hostResolutionRoot(t)
	saveHosts(t, "", "a.example.com", "b.example.com")

	_, err := resolveHost(root, "")
	if err == nil {
		t.Fatal("expected multi-host error")
	}
	var le *LatheError
	if !errors.As(err, &le) {
		t.Fatalf("multi-host error must be structured so the guidance survives rendering, got %T", err)
	}
	if le.Code != CodeGeneral || le.ExitCode != ExitGeneral {
		t.Fatalf("code = %q exit = %d, want %q/%d (unchanged from today)", le.Code, le.ExitCode, CodeGeneral, ExitGeneral)
	}
	if !strings.Contains(le.Message, "a.example.com") || !strings.Contains(le.Message, "b.example.com") {
		t.Fatalf("message must list configured hosts: %q", le.Message)
	}
	if !strings.Contains(le.Hint, "host default set") || !strings.Contains(le.Hint, "DEMO_HOST") {
		t.Fatalf("hint = %q", le.Hint)
	}
}

func TestResolveHostStalePersistedDefault(t *testing.T) {
	root := hostResolutionRoot(t)
	saveHosts(t, "gone.example.com", "prod.example.com")

	stderr := captureStderr(t)
	res, err := resolveHost(root, "codegen.example.com")
	out := readStderr(t, stderr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Hostname != "codegen.example.com" || res.Source != hostSourceCodegenDefault {
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

func TestResolveHostStaleDefaultDoesNotPickSubstitute(t *testing.T) {
	root := hostResolutionRoot(t)
	saveHosts(t, "gone.example.com", "a.example.com", "b.example.com")

	stderr := captureStderr(t)
	res, err := resolveHost(root, "")
	_ = readStderr(t, stderr)
	if err == nil {
		t.Fatalf("expected multi-host error, got %+v", res)
	}
	if res.Hostname != "" {
		t.Fatalf("must not pick a substitute host, got %q", res.Hostname)
	}
	if !strings.Contains(err.Error(), "multiple hosts configured") {
		t.Fatalf("error = %v", err)
	}
}

func TestNoticeImplicitHost(t *testing.T) {
	tests := []struct {
		name string
		res  HostResolution
		want string
	}{
		{"persisted with multiple hosts", HostResolution{Hostname: "staging.example.com", Source: hostSourcePersisted, Configured: 2}, "host: staging.example.com (persisted default)"},
		{"codegen default with multiple hosts", HostResolution{Hostname: "codegen.example.com", Source: hostSourceCodegenDefault, Configured: 2}, "host: codegen.example.com (codegen default)"},
		{"unique single host is silent", HostResolution{Hostname: "only.example.com", Source: hostSourceUnique, Configured: 1}, ""},
		{"explicit flag is silent", HostResolution{Hostname: "flag.example.com", Source: hostSourceFlag, Configured: 2}, ""},
		{"explicit env is silent", HostResolution{Hostname: "env.example.com", Source: hostSourceEnv, Configured: 2}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			noticeImplicitHost(&buf, tt.res)
			if tt.want == "" {
				if buf.Len() != 0 {
					t.Fatalf("notice = %q, want silent", buf.String())
				}
				return
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("notice = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

func TestWriteDryRunIncludesHostProvenance(t *testing.T) {
	var buf bytes.Buffer
	if err := writeDryRun(DryRunRequest{
		Method:     "GET",
		URL:        "https://example.com/x",
		Hostname:   "example.com",
		HostSource: hostSourcePersisted,
	}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"hostname": "example.com"`) || !strings.Contains(buf.String(), `"host_source": "persisted"`) {
		t.Fatalf("dry-run JSON missing provenance:\n%s", buf.String())
	}
}
