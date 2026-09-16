package runtime

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func hostsWith(t *testing.T, selected string, names ...string) *config.Hosts {
	t.Helper()
	t.Setenv("DEMO_CONFIG_DIR", t.TempDir())
	hosts, err := config.LoadHosts()
	testutil.Require(t, err == nil, "LoadHosts: %v", err)
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
			testutil.Require(t, tt.wantErr == (err != nil), "err = %v, wantErr = %v", err, tt.wantErr)
			testutil.Check(t, res.Hostname == tt.wantHost, "hostname = %q, want %q", res.Hostname, tt.wantHost)
			testutil.Check(t, tt.wantErr || res.Source == tt.wantSource, "source = %q, want %q", res.Source, tt.wantSource)
			testutil.Check(t, res.Ambiguous == tt.wantAmbiguous, "ambiguous = %v, want %v", res.Ambiguous, tt.wantAmbiguous)
		})
	}
}

func TestResolveConfiguredHost_MultipleHostErrorStaysVisible(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")
	hosts := hostsWith(t, "", "a.example.com", "b.example.com")

	_, err := resolveConfiguredHost(hosts, "")
	le := ClassifyError(err)
	testutil.Check(t, strings.Contains(le.Message, "a.example.com") && strings.Contains(le.Message, "b.example.com"), "message = %q, want the configured hosts", le.Message)
	testutil.Check(t, strings.Contains(le.Hint, "demo auth use"), "hint = %q, want a pointer at `demo auth use`", le.Hint)
	testutil.Check(t, le.ExitCode == ExitGeneral, "exit = %d, want %d", le.ExitCode, ExitGeneral)
}

func TestLoadHostOptions_UnknownExplicitHostIsActionable(t *testing.T) {
	bindTestManifest(t, "demo", "DEMO_HOST")
	hosts := hostsWith(t, "", "known.example.com")
	testutil.NoError(t, hosts.Save())

	root := &cobra.Command{Use: "demo"}
	root.PersistentFlags().String("hostname", "missing.example.com", "")

	_, _, err := loadHostOptions(root, "", false)
	testutil.Require(t, errors.Is(err, ErrNotAuthenticated), "err = %v, want ErrNotAuthenticated", err)
	le := ClassifyError(err)
	testutil.Require(t, le.Code == CodeNotAuthenticated && le.ExitCode == ExitNotAuthenticated, "classified error = %#v", le)
	testutil.Check(t, le.Message == `no credentials for host "missing.example.com"`, "message = %q", le.Message)
	testutil.Check(t, strings.Contains(le.Hint, "demo auth login --hostname <host>") && strings.Contains(le.Hint, `"known.example.com"`), "hint = %q", le.Hint)
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
