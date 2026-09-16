package lathe

import (
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestResolveVersionPrefersLinkedValues(t *testing.T) {
	version, commit, date := resolveVersion("v1.2.3", "abc123", "2026-01-01", "v9.9.9", "def456", "2026-02-02", true)
	testutil.Require(t, version == "v1.2.3" && commit == "abc123" && date == "2026-01-01", "linked ldflags must win, got %s (%s, %s)", version, commit, date)
}

// `go install <module>@<ref>` links no ldflags; the module version Go records is
// the only thing identifying the build.
func TestResolveVersionFallsBackToModuleVersion(t *testing.T) {
	version, commit, date := resolveVersion("dev", "none", "unknown", "v0.4.6", "", "", false)
	testutil.Check(t, version == "v0.4.6", "want module version v0.4.6, got %q", version)
	testutil.Check(t, commit == "none" && date == "unknown", "without vcs settings commit/date stay unknown, got %q / %q", commit, date)
}

func TestResolveVersionUsesVCSSettingsAndMarksDirty(t *testing.T) {
	_, commit, date := resolveVersion("dev", "none", "unknown", "(devel)", "deadbeef", "2026-07-24T00:00:00Z", true)
	testutil.Check(t, commit == "deadbeef+dirty", "a modified tree must not look like a clean commit, got %q", commit)
	testutil.Check(t, date == "2026-07-24T00:00:00Z", "want vcs.time as date, got %q", date)
}

func TestResolveVersionIgnoresDevelPlaceholder(t *testing.T) {
	version, _, _ := resolveVersion("dev", "none", "unknown", "(devel)", "", "", false)
	testutil.Check(t, version == "dev", "(devel) is not a version, got %q", version)
}
