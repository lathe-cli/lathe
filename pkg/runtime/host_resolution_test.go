package runtime

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
)

func hostsWith(t *testing.T, selected string, names ...string) *config.Hosts {
	t.Helper()
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	for _, n := range names {
		hosts.Set(n, config.HostEntry{AuthType: "bearer", OAuthToken: "t"})
	}
	if selected != "" {
		hosts.Select(selected)
	}
	return hosts
}

func TestResolveConfiguredHost_Order(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")

	tests := []struct {
		name           string
		configured     []string
		selected       string
		codegenDefault string
		wantHost       string
		wantSource     string
		wantAmbiguous  bool
		wantErr        bool
	}{
		{
			name:       "selection wins over codegen default",
			configured: []string{"a.example.com", "b.example.com"},
			selected:   "b.example.com", codegenDefault: "a.example.com",
			wantHost: "b.example.com", wantSource: HostSourceSelected, wantAmbiguous: true,
		},
		{
			name:           "codegen default applies when nothing is selected",
			configured:     []string{"a.example.com", "b.example.com"},
			codegenDefault: "a.example.com",
			wantHost:       "a.example.com", wantSource: HostSourceCodegenDefault, wantAmbiguous: true,
		},
		{
			name:       "single host needs no selection and raises no notice",
			configured: []string{"a.example.com"},
			wantHost:   "a.example.com", wantSource: HostSourceUnique,
		},
		{
			name:       "several hosts and nothing to pick is an error",
			configured: []string{"a.example.com", "b.example.com"},
			wantErr:    true,
		},
		{
			name:    "no hosts at all is not authenticated",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hosts := hostsWith(t, tt.selected, tt.configured...)
			res, err := resolveConfiguredHost(hosts, tt.codegenDefault)
			if tt.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if res.Hostname != tt.wantHost {
				t.Errorf("hostname = %q, want %q", res.Hostname, tt.wantHost)
			}
			if !tt.wantErr && res.Source != tt.wantSource {
				t.Errorf("source = %q, want %q", res.Source, tt.wantSource)
			}
			if res.Ambiguous != tt.wantAmbiguous {
				t.Errorf("ambiguous = %v, want %v", res.Ambiguous, tt.wantAmbiguous)
			}
		})
	}
}

func TestResolveConfiguredHost_MultipleHostErrorStaysVisible(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")
	hosts := hostsWith(t, "", "a.example.com", "b.example.com")

	_, err := resolveConfiguredHost(hosts, "")
	le := ClassifyError(err)
	if !strings.Contains(le.Message, "a.example.com") || !strings.Contains(le.Message, "b.example.com") {
		t.Errorf("message = %q, want the configured hosts", le.Message)
	}
	if !strings.Contains(le.Hint, "demo auth use") {
		t.Errorf("hint = %q, want a pointer at `demo auth use`", le.Hint)
	}
	if le.ExitCode != ExitGeneral {
		t.Errorf("exit = %d, want %d", le.ExitCode, ExitGeneral)
	}
}

func TestHostReporterNoticesAnAmbiguousHostOnce(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")
	var buf bytes.Buffer
	var r hostReporter

	r.noticeImplicitHost(&buf, HostResolution{Hostname: "b.example.com", Source: HostSourceSelected, Ambiguous: true})
	r.noticeImplicitHost(&buf, HostResolution{Hostname: "b.example.com", Source: HostSourceSelected, Ambiguous: true})
	if got := buf.String(); got != "current host: b.example.com\n" {
		t.Errorf("output = %q, want one notice", got)
	}
}
