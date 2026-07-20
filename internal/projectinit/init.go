package projectinit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const manifestName = ".lathe-template.yaml"

var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	cliPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

var defaultTemplates = map[string]string{
	"node":   "https://github.com/lathe-cli/lathe-node-starter.git",
	"go":     "https://github.com/lathe-cli/lathe-go-starter.git",
	"python": "https://github.com/lathe-cli/lathe-python-starter.git",
	"rust":   "https://github.com/lathe-cli/lathe-rust-starter.git",
}

type Options struct {
	Target        string
	Language      string
	Template      string
	AppName       string
	CLIName       string
	GoModule      string
	License       string
	LicenseHolder string
	LatheVersion  string
	Stderr        io.Writer
	Bootstrap     func(projectRoot string, output io.Writer) error
}

type Result struct {
	SchemaVersion int            `json:"schema_version"`
	Path          string         `json:"path"`
	Language      string         `json:"language"`
	AppName       string         `json:"app_name"`
	CLIName       string         `json:"cli_name"`
	License       string         `json:"license"`
	Template      TemplateResult `json:"template"`
	Git           GitResult      `json:"git"`
	NextCommand   string         `json:"next_command"`
}

type TemplateResult struct {
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

type GitResult struct {
	Branch      string `json:"branch"`
	HasCommits  bool   `json:"has_commits"`
	HasRemote   bool   `json:"has_remote"`
	StagedFiles int    `json:"staged_files"`
}

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

func Init(opts Options) (Result, error) {
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Bootstrap == nil {
		return Result{}, errors.New("missing bootstrap runner")
	}
	if _, ok := defaultTemplates[opts.Language]; !ok {
		return Result{}, fmt.Errorf("unsupported language %q", opts.Language)
	}

	target, values, err := resolveInputs(opts)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return Result{}, fmt.Errorf("target already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect target: %w", err)
	}
	parent := filepath.Dir(target)
	info, err := os.Stat(parent)
	if err != nil {
		return Result{}, fmt.Errorf("inspect target parent: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("target parent is not a directory: %s", parent)
	}

	repo, ref, err := resolveTemplate(opts.Language, opts.Template)
	if err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp(parent, ".lathe-init-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()

	if err := run(opts.Stderr, "", "git", "clone", "--filter=blob:none", "--single-branch", "--branch", ref, "--", repo, tmp); err != nil {
		return Result{}, fmt.Errorf("clone template: %w", err)
	}
	commit, err := output("", "git", "-C", tmp, "rev-parse", "HEAD")
	if err != nil || !commitPattern.MatchString(commit) {
		return Result{}, errors.New("resolve template commit")
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return Result{}, fmt.Errorf("remove template git metadata: %w", err)
	}
	if err := rejectSymlinks(tmp); err != nil {
		return Result{}, err
	}

	manifest, err := loadManifest(tmp)
	if err != nil {
		return Result{}, err
	}
	if manifest.Language != opts.Language {
		return Result{}, fmt.Errorf("template language %q does not match %q", manifest.Language, opts.Language)
	}
	if values["lathe_version"] == "" {
		values["lathe_version"] = manifest.Defaults["lathe_version"]
	}
	if err := renderTemplate(tmp, manifest, values); err != nil {
		return Result{}, err
	}
	if err := writeLicense(tmp, opts.License, values["license_holder"]); err != nil {
		return Result{}, err
	}
	if err := opts.Bootstrap(tmp, opts.Stderr); err != nil {
		return Result{}, fmt.Errorf("bootstrap generated CLI: %w", err)
	}
	if err := run(opts.Stderr, tmp, "go", "mod", "tidy"); err != nil {
		return Result{}, fmt.Errorf("tidy generated CLI module: %w", err)
	}
	for _, path := range manifest.Cleanup {
		if err := removeLocalPath(tmp, path); err != nil {
			return Result{}, fmt.Errorf("clean %s: %w", path, err)
		}
	}
	if err := os.Remove(filepath.Join(tmp, manifestName)); err != nil {
		return Result{}, fmt.Errorf("remove template manifest: %w", err)
	}
	if err := run(opts.Stderr, tmp, "git", "init", "-b", "main"); err != nil {
		return Result{}, fmt.Errorf("initialize git repository: %w", err)
	}
	if err := verifyRepository(tmp); err != nil {
		return Result{}, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return Result{}, fmt.Errorf("move initialized repository into place: %w", err)
	}
	keep = true

	return Result{
		SchemaVersion: 1,
		Path:          target,
		Language:      opts.Language,
		AppName:       values["app_name"],
		CLIName:       values["cli_name"],
		License:       opts.License,
		Template:      TemplateResult{Repo: repo, Ref: ref, Commit: commit},
		Git:           GitResult{Branch: "main"},
		NextCommand:   checkCommand(manifest.CheckProfile),
	}, nil
}

func resolveInputs(opts Options) (string, map[string]string, error) {
	if opts.Target == "" {
		return "", nil, errors.New("target directory is required")
	}
	target, err := filepath.Abs(opts.Target)
	if err != nil {
		return "", nil, fmt.Errorf("resolve target: %w", err)
	}
	slug := filepath.Base(filepath.Clean(target))
	if !slugPattern.MatchString(slug) {
		return "", nil, fmt.Errorf("target directory name must match %s", slugPattern)
	}
	if opts.AppName == "" {
		opts.AppName = slug
	}
	if opts.CLIName == "" {
		opts.CLIName = slug + "ctl"
	}
	if opts.GoModule == "" {
		opts.GoModule = "example.com/" + slug
	}
	if opts.License == "" {
		opts.License = "mit"
	}
	if opts.LicenseHolder == "" {
		opts.LicenseHolder = opts.AppName + " contributors"
	}
	if !cliPattern.MatchString(opts.CLIName) {
		return "", nil, fmt.Errorf("CLI name must match %s", cliPattern)
	}
	if hasControl(opts.AppName) || hasControl(opts.LicenseHolder) {
		return "", nil, errors.New("application name and license holder must not contain control characters")
	}
	if strings.TrimSpace(opts.AppName) == "" || strings.TrimSpace(opts.LicenseHolder) == "" {
		return "", nil, errors.New("application name and license holder must not be empty")
	}
	if strings.HasPrefix(opts.GoModule, "/") || strings.ContainsAny(opts.GoModule, "@ \t\r\n") {
		return "", nil, fmt.Errorf("invalid Go module path %q", opts.GoModule)
	}
	if opts.License != "mit" && opts.License != "none" {
		return "", nil, fmt.Errorf("unsupported license %q", opts.License)
	}
	if opts.LatheVersion != "" && opts.LatheVersion != "dev" && !versionPattern.MatchString(opts.LatheVersion) {
		return "", nil, fmt.Errorf("invalid Lathe version %q", opts.LatheVersion)
	}
	version := ""
	if versionPattern.MatchString(opts.LatheVersion) {
		version = opts.LatheVersion
	}
	return target, map[string]string{
		"app_name":       opts.AppName,
		"app_slug":       slug,
		"package_ident":  strings.ReplaceAll(slug, "-", "_"),
		"cli_name":       opts.CLIName,
		"go_module":      opts.GoModule,
		"license_holder": opts.LicenseHolder,
		"lathe_version":  version,
	}, nil
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

func verifyRepository(root string) error {
	if _, err := output(root, "git", "rev-parse", "--verify", "HEAD"); err == nil {
		return errors.New("initialized repository unexpectedly has a commit")
	}
	remotes, err := output(root, "git", "remote")
	if err != nil {
		return fmt.Errorf("inspect initialized remotes: %w", err)
	}
	if remotes != "" {
		return errors.New("initialized repository unexpectedly has a remote")
	}
	staged, err := output(root, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return fmt.Errorf("inspect staged files: %w", err)
	}
	if staged != "" {
		return errors.New("initialized repository unexpectedly has staged files")
	}
	status, err := output(root, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect initialized repository: %w", err)
	}
	if status == "" {
		return errors.New("initialized repository has no application files")
	}
	return nil
}

func run(outputWriter io.Writer, dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	return cmd.Run()
}

func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	data, err := cmd.Output()
	return strings.TrimSpace(string(data)), err
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

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
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
