package specsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"gopkg.in/yaml.v3"
)

type goModuleDownload struct {
	Dir   string
	Sum   string
	Error string
}

type bufLock struct {
	Deps []struct {
		Name   string `yaml:"name"`
		Commit string `yaml:"commit"`
		Digest string `yaml:"digest"`
	} `yaml:"deps"`
}

func stageProtoDependencies(src *sourceconfig.Source, syncDir, workRoot string) error {
	for _, dep := range src.Proto.Dependencies {
		root, err := materializeProtoDependency(dep, workRoot)
		if err != nil {
			return err
		}
		for _, st := range dep.Staging {
			from, err := safeJoin(root, st.From)
			if err != nil {
				return err
			}
			to, err := safeJoin(syncDir, st.To)
			if err != nil {
				return err
			}
			if err := copyProtoTree(from, to); err != nil {
				return fmt.Errorf("dependency %s staging %s -> %s: %w", dependencyName(dep), st.From, st.To, err)
			}
		}
	}
	return nil
}

func materializeProtoDependency(dep sourceconfig.ProtoDependency, workRoot string) (string, error) {
	switch dep.Kind {
	case sourceconfig.ProtoDependencyGit:
		root := repoWorkDir(workRoot, dep.RepoURL, dep.PinnedTag)
		if _, err := ensureRepo(root, dep.RepoURL, dep.PinnedTag); err != nil {
			return "", fmt.Errorf("dependency %s: %w", dependencyName(dep), err)
		}
		return root, nil
	case sourceconfig.ProtoDependencyGoModule:
		return downloadGoModule(dep, workRoot)
	case sourceconfig.ProtoDependencyBuf:
		return exportBufModule(dep, workRoot)
	default:
		return "", fmt.Errorf("unsupported proto dependency kind %q", dep.Kind)
	}
}

func downloadGoModule(dep sourceconfig.ProtoDependency, workRoot string) (string, error) {
	cache := filepath.Join(workRoot, "go-mod-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "mod", "download", "-json", dep.Module+"@"+dep.Version)
	// -modcacherw keeps the cache under the lathe cache root removable with rm -rf.
	cmd.Env = append(os.Environ(), "GOMODCACHE="+cache, "GOFLAGS="+strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("dependency %s: go mod download: %w: %s", dependencyName(dep), err, strings.TrimSpace(stderr.String()))
	}
	var result goModuleDownload
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", fmt.Errorf("dependency %s: decode go mod download: %w", dependencyName(dep), err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("dependency %s: go mod download: %s", dependencyName(dep), result.Error)
	}
	if result.Dir == "" {
		return "", fmt.Errorf("dependency %s: go mod download returned no directory", dependencyName(dep))
	}
	if result.Sum != dep.Sum {
		return "", fmt.Errorf("dependency %s: checksum %q does not match configured %q", dependencyName(dep), result.Sum, dep.Sum)
	}
	return result.Dir, nil
}

func exportBufModule(dep sourceconfig.ProtoDependency, workRoot string) (string, error) {
	key := dep.Module + "\x00" + dep.Commit + "\x00" + dep.Digest
	root := filepath.Join(workRoot, fmt.Sprintf("buf-%x", sha256.Sum256([]byte(key))))
	marker := filepath.Join(root, ".lathe-buf-digest")
	// root is keyed by the configured digest, so a published marker always
	// describes an export that matched it.
	if _, err := os.Stat(marker); err == nil {
		return root, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := verifyBufDependency(dep, workRoot); err != nil {
		return "", err
	}

	tmp, err := os.MkdirTemp(workRoot, ".buf-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	cmd := exec.Command("buf", "export", dep.Module+":"+dep.Commit, "-o", tmp)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("dependency %s: buf export: %w", dependencyName(dep), err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".lathe-buf-digest"), []byte(dep.Digest+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, root); err != nil {
		if _, statErr := os.Stat(marker); statErr != nil {
			return "", fmt.Errorf("dependency %s: publish buf export: %w", dependencyName(dep), err)
		}
	}
	return root, nil
}

func verifyBufDependency(dep sourceconfig.ProtoDependency, workRoot string) error {
	dir, err := os.MkdirTemp(workRoot, ".buf-verify-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	local := filepath.Join(dir, "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		return err
	}
	config := fmt.Sprintf("version: v2\nmodules:\n  - path: local\ndeps:\n  - %s:%s\n", dep.Module, dep.Commit)
	if err := os.WriteFile(filepath.Join(dir, "buf.yaml"), []byte(config), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(local, "empty.proto"), []byte("syntax = \"proto3\";\npackage lathe.verify;\n"), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("buf", "dep", "update", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dependency %s: verify buf lock: %w: %s", dependencyName(dep), err, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(filepath.Join(dir, "buf.lock"))
	if err != nil {
		return fmt.Errorf("dependency %s: read verified buf.lock: %w", dependencyName(dep), err)
	}
	var lock bufLock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return fmt.Errorf("dependency %s: decode verified buf.lock: %w", dependencyName(dep), err)
	}
	for _, pin := range lock.Deps {
		if pin.Name == dep.Module && pin.Commit == dep.Commit {
			if pin.Digest != dep.Digest {
				return fmt.Errorf("dependency %s: digest %q does not match configured %q", dependencyName(dep), pin.Digest, dep.Digest)
			}
			return nil
		}
	}
	return fmt.Errorf("dependency %s: verified buf.lock did not contain configured pin", dependencyName(dep))
}

func dependencyName(dep sourceconfig.ProtoDependency) string {
	switch dep.Kind {
	case sourceconfig.ProtoDependencyGit:
		return dep.RepoURL + "@" + dep.PinnedTag
	case sourceconfig.ProtoDependencyGoModule:
		return dep.Module + "@" + dep.Version
	case sourceconfig.ProtoDependencyBuf:
		return dep.Module + ":" + dep.Commit
	default:
		return dep.Kind
	}
}
