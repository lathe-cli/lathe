package runtime

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

type SourceProvenance struct {
	ID           string `json:"id"`
	Backend      string `json:"backend"`
	Kind         string `json:"kind"`
	RepoURL      string `json:"repo_url,omitempty"`
	PinnedTag    string `json:"pinned_tag,omitempty"`
	ResolvedSHA  string `json:"resolved_sha,omitempty"`
	Reproducible bool   `json:"reproducible"`
}

const sourceProvenanceAnnotation = "lathe.provenance.sources"

func AttachSourceProvenance(root *cobra.Command, sources []SourceProvenance) {
	if root == nil {
		return
	}
	if sources == nil {
		sources = []SourceProvenance{}
	}
	raw, err := json.Marshal(sources)
	if err != nil {
		panic(err)
	}
	if root.Annotations == nil {
		root.Annotations = map[string]string{}
	}
	root.Annotations[sourceProvenanceAnnotation] = string(raw)
}

func SourceProvenanceOf(root *cobra.Command) []SourceProvenance {
	if root == nil || root.Annotations == nil {
		return nil
	}
	raw, ok := root.Annotations[sourceProvenanceAnnotation]
	if !ok || raw == "" {
		return nil
	}
	var sources []SourceProvenance
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return nil
	}
	return sources
}
