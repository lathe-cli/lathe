package specsync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

const (
	WorkSubdir = "specs-work"
	SyncSubdir = "specs-sync"
)

type Options struct {
	CacheRoot string
	Filter    string
}

func Sync(cfg *sourceconfig.Config, opts Options) error {
	workRoot := filepath.Join(opts.CacheRoot, WorkSubdir)
	syncRoot := filepath.Join(opts.CacheRoot, SyncSubdir)
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(syncRoot, 0o755); err != nil {
		return err
	}
	for _, src := range cfg.Ordered() {
		if opts.Filter != "" && src.Name != opts.Filter {
			continue
		}
		workDir := filepath.Join(workRoot, src.Name)
		syncDir := filepath.Join(syncRoot, src.Name)
		if err := os.RemoveAll(syncDir); err != nil {
			return err
		}
		sourceDir := workDir
		sha := ""
		if src.LocalPath != "" {
			sourceDir = src.LocalPath
		} else {
			var err error
			sha, err = ensureRepo(workDir, src.RepoURL, src.PinnedTag)
			if err != nil {
				return fmt.Errorf("source %q: %w", src.Name, err)
			}
		}
		switch src.Backend {
		case sourceconfig.BackendSwagger:
			if err := syncSwagger(src, sourceDir, syncDir); err != nil {
				return fmt.Errorf("source %q: %w", src.Name, err)
			}
		case sourceconfig.BackendProto:
			if err := syncProto(src, sourceDir, syncDir); err != nil {
				return fmt.Errorf("source %q: %w", src.Name, err)
			}
		case sourceconfig.BackendOpenAPI3:
			if err := syncOpenAPI3(src, sourceDir, syncDir); err != nil {
				return fmt.Errorf("source %q: %w", src.Name, err)
			}
		case sourceconfig.BackendGraphQL:
			if err := syncGraphQL(src, sourceDir, syncDir); err != nil {
				return fmt.Errorf("source %q: %w", src.Name, err)
			}
		default:
			return fmt.Errorf("source %q: unsupported backend %q", src.Name, src.Backend)
		}
		state := &State{
			Source:      src.Name,
			Backend:     src.Backend,
			SyncedFrom:  src.PinnedTag,
			ResolvedSHA: sha,
		}
		if src.LocalPath != "" {
			state.SourceKind = SourceKindLocal
			state.SyncedFrom = src.LocalPath
		}
		if err := SaveState(syncDir, state); err != nil {
			return fmt.Errorf("source %q: write sync-state: %w", src.Name, err)
		}
	}
	return nil
}
