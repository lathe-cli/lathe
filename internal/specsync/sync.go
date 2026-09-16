package specsync

import (
	"crypto/sha256"
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
	checkouts := make(map[string]string)
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
		syncDir := filepath.Join(syncRoot, src.Name)
		if err := os.RemoveAll(syncDir); err != nil {
			return err
		}
		sourceDir := src.LocalPath
		sha := ""
		if src.LocalPath == "" {
			key := src.RepoURL + "\x00" + src.PinnedTag
			sourceDir = repoWorkDir(workRoot, src.RepoURL, src.PinnedTag)
			if cached, ok := checkouts[key]; ok {
				sha = cached
			} else {
				var err error
				sha, err = ensureRepo(sourceDir, src.RepoURL, src.PinnedTag)
				if err != nil {
					return fmt.Errorf("source %q: %w", src.Name, err)
				}
				checkouts[key] = sha
			}
		}
		if err := syncSource(src, sourceDir, syncDir, workRoot); err != nil {
			return fmt.Errorf("source %q: %w", src.Name, err)
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

func repoWorkDir(workRoot, repoURL, ref string) string {
	key := repoURL + "\x00" + ref
	return filepath.Join(workRoot, fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
}

func syncSource(src *sourceconfig.Source, sourceDir, syncDir, workRoot string) error {
	switch src.Backend {
	case sourceconfig.BackendSwagger:
		return syncFiles(src, src.Swagger.Files, sourceDir, syncDir)
	case sourceconfig.BackendOpenAPI3:
		return syncFiles(src, src.OpenAPI3.Files, sourceDir, syncDir)
	case sourceconfig.BackendGraphQL:
		return syncFiles(src, []string{src.GraphQL.Schema}, sourceDir, syncDir)
	case sourceconfig.BackendProto:
		return syncProto(src, sourceDir, syncDir, workRoot)
	default:
		return fmt.Errorf("unsupported backend %q", src.Backend)
	}
}
