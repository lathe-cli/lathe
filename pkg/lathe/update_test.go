package lathe

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
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

	if err := runGitHubUpdate(cmd, m, false); err != nil {
		t.Fatalf("runGitHubUpdate: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "demo is newer than latest release (v0.1.3-0.20260830061345-0837499497e7 > v0.1.2)") {
		t.Fatalf("output = %q", got)
	}
}
