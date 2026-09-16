package projectinit

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

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
	opts.AppName = cmp.Or(opts.AppName, slug)
	opts.CLIName = cmp.Or(opts.CLIName, slug+"ctl")
	opts.GoModule = cmp.Or(opts.GoModule, "example.com/"+slug)
	opts.License = cmp.Or(opts.License, "mit")
	opts.LicenseHolder = cmp.Or(opts.LicenseHolder, opts.AppName+" contributors")
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

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
