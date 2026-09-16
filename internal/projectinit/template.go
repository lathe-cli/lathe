package projectinit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const manifestName = ".lathe-template.yaml"

type templateManifest struct {
	SchemaVersion int               `yaml:"schema_version"`
	Language      string            `yaml:"language"`
	Defaults      map[string]string `yaml:"defaults"`
	Replacements  []replacement     `yaml:"replacements"`
	Renames       []rename          `yaml:"renames"`
	Generated     []string          `yaml:"generated"`
	Cleanup       []string          `yaml:"cleanup"`
	CheckProfile  string            `yaml:"check_profile"`
}

type replacement struct {
	Variable string   `yaml:"variable"`
	From     string   `yaml:"from"`
	Files    []string `yaml:"files"`
}

type rename struct {
	From     string `yaml:"from"`
	Variable string `yaml:"variable"`
}

func resolveTemplate(language, value string) (string, string, error) {
	if value == "" {
		value = defaultTemplates[language]
	}
	repo, ref := value, "main"
	if i := strings.LastIndex(value, "#"); i >= 0 {
		repo, ref = value[:i], value[i+1:]
	}
	if repo == "" || ref == "" {
		return "", "", errors.New("template must be <git-url>[#<ref>]")
	}
	if err := exec.Command("git", "check-ref-format", "--branch", ref).Run(); err != nil {
		return "", "", fmt.Errorf("invalid template ref %q", ref)
	}
	return repo, ref, nil
}

func loadManifest(root string) (*templateManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil {
		return nil, fmt.Errorf("read template manifest: %w", err)
	}
	var manifest templateManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse template manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported template schema version %d", manifest.SchemaVersion)
	}
	if _, ok := defaultTemplates[manifest.Language]; !ok {
		return nil, fmt.Errorf("unsupported template language %q", manifest.Language)
	}
	if checkCommand(manifest.CheckProfile) == "" {
		return nil, fmt.Errorf("unsupported check profile %q", manifest.CheckProfile)
	}
	return &manifest, nil
}

func renderTemplate(root string, manifest *templateManifest, values map[string]string) error {
	for _, item := range manifest.Replacements {
		value, ok := values[item.Variable]
		if !ok || value == "" {
			return fmt.Errorf("missing template variable %q", item.Variable)
		}
		from := item.From
		if from == "" {
			from = manifest.Defaults[item.Variable]
		}
		if from == "" || len(item.Files) == 0 {
			return fmt.Errorf("invalid replacement for %q", item.Variable)
		}
		for _, name := range item.Files {
			path, err := localPath(root, name)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read replacement target %s: %w", name, err)
			}
			if !bytes.Contains(data, []byte(from)) {
				return fmt.Errorf("replacement token %q not found in %s", from, name)
			}
			if bytes.IndexByte(data, 0) >= 0 {
				return fmt.Errorf("replacement target is not text: %s", name)
			}
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("inspect replacement target %s: %w", name, err)
			}
			if err := os.WriteFile(path, bytes.ReplaceAll(data, []byte(from), []byte(value)), info.Mode().Perm()); err != nil {
				return fmt.Errorf("write replacement target %s: %w", name, err)
			}
		}
	}
	for _, item := range manifest.Renames {
		value := values[item.Variable]
		if value == "" {
			return fmt.Errorf("missing rename variable %q", item.Variable)
		}
		from, err := localPath(root, item.From)
		if err != nil {
			return err
		}
		to, err := localPath(root, filepath.Join(filepath.Dir(item.From), value))
		if err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("rename %s: %w", item.From, err)
		}
	}
	for _, name := range manifest.Generated {
		if err := removeLocalPath(root, name); err != nil {
			return fmt.Errorf("remove generated path %s: %w", name, err)
		}
	}
	return nil
}

func localPath(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("invalid template path %q", name)
	}
	return filepath.Join(root, clean), nil
}

func removeLocalPath(root, name string) error {
	path, err := localPath(root, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func rejectSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			rel, _ := filepath.Rel(root, path)
			return fmt.Errorf("template symlink is not allowed: %s", rel)
		}
		return nil
	})
}

func writeLicense(root, kind, holder string) error {
	path := filepath.Join(root, "LICENSE")
	if kind == "none" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove license: %w", err)
		}
		return nil
	}
	text := fmt.Sprintf(mitLicense, time.Now().Year(), holder)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("write license: %w", err)
	}
	return nil
}

func checkCommand(profile string) string {
	switch profile {
	case "pnpm":
		return "pnpm check"
	case "make":
		return "make check"
	default:
		return ""
	}
}

const mitLicense = `MIT License

Copyright (c) %d %s

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
