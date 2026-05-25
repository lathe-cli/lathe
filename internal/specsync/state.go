package specsync

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const StateFile = "sync-state.yaml"

type State struct {
	Source      string `yaml:"source"`
	Backend     string `yaml:"backend"`
	SyncedFrom  string `yaml:"synced_from"`
	ResolvedSHA string `yaml:"resolved_sha"`
}

func LoadState(syncDir string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(syncDir, StateFile))
	if err != nil {
		return nil, err
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func SaveState(syncDir string, s *State) error {
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(syncDir, StateFile), data, 0o644)
}

func VerifyState(syncDir, source, backend, wantTag string) error {
	s, err := LoadState(syncDir)
	if err != nil {
		return fmt.Errorf("source %q: sync-state missing or unreadable (run `lathe specsync`): %w", source, err)
	}
	if s.Source != source {
		return fmt.Errorf("source %q: sync-state mismatch (got %q)", source, s.Source)
	}
	if s.Backend != backend {
		return fmt.Errorf("source %q: sync-state backend %q != config %q (re-run `lathe specsync`)", source, s.Backend, backend)
	}
	if s.SyncedFrom != wantTag {
		return fmt.Errorf("source %q: synced_from=%q but pinned_tag=%q (re-run `lathe specsync`)", source, s.SyncedFrom, wantTag)
	}
	if s.ResolvedSHA == "" {
		return fmt.Errorf("source %q: sync-state missing resolved_sha (re-run `lathe specsync`)", source)
	}
	return nil
}
