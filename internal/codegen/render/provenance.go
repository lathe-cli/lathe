package render

import (
	"slices"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/specsync"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func SourceProvenance(src *sourceconfig.Source, state *specsync.State) runtime.SourceProvenance {
	p := runtime.SourceProvenance{
		ID:      src.Name,
		Backend: src.Backend,
	}
	if src.LocalPath != "" {
		p.Kind = specsync.SourceKindLocal
		return p
	}
	p.Kind = specsync.SourceKindGit
	p.PinnedTag = src.PinnedTag
	if state != nil {
		p.RepoURL = state.RepoURL
		p.ResolvedSHA = state.ResolvedSHA
	}
	p.Reproducible = p.ResolvedSHA != "" && p.RepoURL != "" && !hasGitProtoDependency(src)
	return p
}

func hasGitProtoDependency(src *sourceconfig.Source) bool {
	if src.Proto == nil {
		return false
	}
	return slices.ContainsFunc(src.Proto.Dependencies, func(d sourceconfig.ProtoDependency) bool {
		return d.Kind == sourceconfig.ProtoDependencyGit
	})
}
