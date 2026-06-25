package specsync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"gopkg.in/yaml.v3"
)

const StateFile = "sync-state.yaml"

const (
	SourceKindGit   = "git"
	SourceKindLocal = "local"
)

type State struct {
	SourceKind  string `yaml:"source_kind,omitempty"`
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

func VerifyState(syncDir string, src *sourceconfig.Source) error {
	s, err := LoadState(syncDir)
	if err != nil {
		return fmt.Errorf("source %q: sync-state missing or unreadable (run `lathe specsync`): %w", src.Name, err)
	}
	if s.Source != src.Name {
		return fmt.Errorf("source %q: sync-state mismatch (got %q)", src.Name, s.Source)
	}
	if s.Backend != src.Backend {
		return fmt.Errorf("source %q: sync-state backend %q != config %q (re-run `lathe specsync`)", src.Name, s.Backend, src.Backend)
	}
	wantKind := SourceKindGit
	wantFrom := src.PinnedTag
	if src.LocalPath != "" {
		wantKind = SourceKindLocal
		wantFrom = src.LocalPath
	}
	gotKind := s.SourceKind
	if gotKind == "" {
		gotKind = SourceKindGit
	}
	if gotKind != wantKind {
		return fmt.Errorf("source %q: sync-state source_kind %q != config %q (re-run `lathe specsync`)", src.Name, gotKind, wantKind)
	}
	if s.SyncedFrom != wantFrom {
		if wantKind == SourceKindLocal {
			return fmt.Errorf("source %q: synced_from=%q but local_path=%q (re-run `lathe specsync`)", src.Name, s.SyncedFrom, wantFrom)
		}
		return fmt.Errorf("source %q: synced_from=%q but pinned_tag=%q (re-run `lathe specsync`)", src.Name, s.SyncedFrom, wantFrom)
	}
	if wantKind == SourceKindGit && s.ResolvedSHA == "" {
		return fmt.Errorf("source %q: sync-state missing resolved_sha (re-run `lathe specsync`)", src.Name)
	}
	return nil
}
