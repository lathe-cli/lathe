package specsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"

	"github.com/lathe-cli/lathe/internal/testutil"
)

const fakeSHA = "1234567890abcdef1234567890abcdef12345678"

func gitSource() *sourceconfig.Source {
	return &sourceconfig.Source{
		Name:      "demo",
		Backend:   sourceconfig.BackendSwagger,
		PinnedTag: "v1.2.3",
	}
}

func TestSaveLoadState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &State{
		Source:      "demo",
		Backend:     "swagger",
		SyncedFrom:  "v1.2.3",
		ResolvedSHA: fakeSHA,
	}
	testutil.NoError(t, SaveState(dir, want))
	got, err := LoadState(dir)
	testutil.Require(t, err == nil, "LoadState: %v", err)
	testutil.Check(t, *got == *want, "round trip mismatch:\n got = %+v\nwant = %+v", got, want)
}

func TestVerifyState_AcceptsFullState(t *testing.T) {
	dir := t.TempDir()
	testutil.NoError(t, SaveState(dir, &State{
		Source:      "demo",
		Backend:     "swagger",
		SyncedFrom:  "v1.2.3",
		ResolvedSHA: fakeSHA,
	}))
	if err := VerifyState(dir, gitSource()); err != nil {
		t.Errorf("VerifyState: %v", err)
	}
}

func TestVerifyState_AcceptsLocalState(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(t.TempDir(), "api")
	testutil.NoError(t, SaveState(dir, &State{
		SourceKind: SourceKindLocal,
		Source:     "demo",
		Backend:    "openapi3",
		SyncedFrom: localPath,
	}))
	src := &sourceconfig.Source{
		Name:      "demo",
		Backend:   sourceconfig.BackendOpenAPI3,
		LocalPath: localPath,
	}
	if err := VerifyState(dir, src); err != nil {
		t.Errorf("VerifyState: %v", err)
	}
}

func TestVerifyState_RejectsMissingResolvedSHA(t *testing.T) {
	dir := t.TempDir()
	// Simulate an old sync-state.yaml written before T2.2 landed — no
	// resolved_sha field.
	legacy := "source: demo\nbackend: swagger\nsynced_from: v1.2.3\n"
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, StateFile), []byte(legacy), 0o644))
	err := VerifyState(dir, gitSource())
	testutil.Require(t, err != nil, "VerifyState accepted state missing resolved_sha")
	testutil.Check(t, strings.Contains(err.Error(), "resolved_sha"), "error should mention resolved_sha: %v", err)
}

func TestVerifyState_RejectsStaleTag(t *testing.T) {
	dir := t.TempDir()
	testutil.NoError(t, SaveState(dir, &State{
		Source:      "demo",
		Backend:     "swagger",
		SyncedFrom:  "v1.0.0",
		ResolvedSHA: fakeSHA,
	}))
	err := VerifyState(dir, gitSource())
	testutil.Require(t, err != nil, "VerifyState accepted stale tag")
	testutil.Check(t, strings.Contains(err.Error(), "pinned_tag"), "error should mention pinned_tag mismatch: %v", err)
}

func TestVerifyState_RejectsMissingFile(t *testing.T) {
	dir := t.TempDir() // empty
	err := VerifyState(dir, gitSource())
	testutil.Require(t, err != nil, "VerifyState accepted missing sync-state")
	testutil.Check(t, strings.Contains(err.Error(), "lathe specsync"), "error should tell user to run lathe specsync: %v", err)
}
