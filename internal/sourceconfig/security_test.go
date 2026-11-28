package sourceconfig

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadRejectsUnsafeRepoURLs(t *testing.T) {
	for _, repo := range []string{"--upload-pack=malicious", "ext::sh -c payload", "ftp://example.com/repo", "custom::payload"} {
		for _, dependency := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dependency=%t", repo, dependency), func(t *testing.T) {
				body := fmt.Sprintf("sources:\n  demo:\n    repo_url: %q\n    pinned_tag: v1.0.0\n    backend: swagger\n    swagger:\n      files: [api.json]\n", repo)
				if dependency {
					body = fmt.Sprintf("sources:\n  demo:\n    local_path: .\n    backend: proto\n    proto:\n      staging: [{from: ., to: .}]\n      entries: [api.proto]\n      dependencies:\n        - kind: git\n          repo_url: %q\n          pinned_tag: v1.0.0\n          staging: [{from: ., to: .}]\n", repo)
				}
				if _, err := loadSources(t, body); err == nil {
					t.Fatal("unsafe repo_url accepted")
				}
			})
		}
	}
}

func TestLoadRequiresGoSourceIdentifier(t *testing.T) {
	for _, name := range []string{"api-name", "api.name", "2api", "type", "range", "API", "_", "ſpec"} {
		t.Run(name, func(t *testing.T) {
			body := fmt.Sprintf("sources:\n  %q:\n    local_path: .\n    backend: swagger\n    swagger:\n      files: [api.json]\n", name)
			if _, err := loadSources(t, body); err == nil {
				t.Fatal("invalid source identifier accepted")
			}
		})
	}
}

func TestLoadRejectsUnknownFieldsWithoutContent(t *testing.T) {
	base := "sources:\n  demo:\n    local_path: .\n    backend: swagger\n    swagger:\n      files: [api.json]\n"
	for _, body := range []string{base + "    pinned_tga: canary\n", strings.Replace(base, "files: [api.json]", "files: [api.json]\n      fle: canary", 1), base + "canary: true\n", strings.Replace(base, "files: [api.json]", "files: canary", 1)} {
		_, err := loadSources(t, body)
		if err == nil {
			t.Error("invalid YAML accepted")
			continue
		}
		if strings.Contains(err.Error(), "canary") {
			t.Error("YAML error disclosed file content")
		}
	}
}
