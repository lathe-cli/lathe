package specsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeSHA = "1234567890abcdef1234567890abcdef12345678"

func TestSaveLoadState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &State{
		Source:      "demo",
		Backend:     "swagger",
		SyncedFrom:  "v1.2.3",
		ResolvedSHA: fakeSHA,
	}
	if err := SaveState(dir, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if *got != *want {
		t.Errorf("round trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestVerifyState_AcceptsFullState(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{
		Source:      "demo",
		Backend:     "swagger",
		SyncedFrom:  "v1.2.3",
		ResolvedSHA: fakeSHA,
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := VerifyState(dir, "demo", "swagger", "v1.2.3"); err != nil {
		t.Errorf("VerifyState: %v", err)
	}
}

func TestVerifyState_RejectsMissingResolvedSHA(t *testing.T) {
	dir := t.TempDir()
	// Simulate an old sync-state.yaml written before T2.2 landed — no
	// resolved_sha field.
	legacy := "source: demo\nbackend: swagger\nsynced_from: v1.2.3\n"
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	err := VerifyState(dir, "demo", "swagger", "v1.2.3")
	if err == nil {
		t.Fatalf("VerifyState accepted state missing resolved_sha")
	}
	if !strings.Contains(err.Error(), "resolved_sha") {
		t.Errorf("error should mention resolved_sha: %v", err)
	}
}

func TestVerifyState_RejectsStaleTag(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{
		Source:      "demo",
		Backend:     "swagger",
		SyncedFrom:  "v1.0.0",
		ResolvedSHA: fakeSHA,
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	err := VerifyState(dir, "demo", "swagger", "v1.2.3")
	if err == nil {
		t.Fatalf("VerifyState accepted stale tag")
	}
	if !strings.Contains(err.Error(), "pinned_tag") {
		t.Errorf("error should mention pinned_tag mismatch: %v", err)
	}
}

func TestVerifyState_RejectsMissingFile(t *testing.T) {
	dir := t.TempDir() // empty
	err := VerifyState(dir, "demo", "swagger", "v1.2.3")
	if err == nil {
		t.Fatalf("VerifyState accepted missing sync-state")
	}
	if !strings.Contains(err.Error(), "lathe specsync") {
		t.Errorf("error should tell user to run lathe specsync: %v", err)
	}
}
