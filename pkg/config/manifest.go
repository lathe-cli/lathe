package config

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	CLI    CLIInfo    `yaml:"cli"`
	Auth   AuthInfo   `yaml:"auth"`
	Update UpdateInfo `yaml:"update,omitempty"`
}

type CLIInfo struct {
	Name         string `yaml:"name"`
	Short        string `yaml:"short"`
	ConfigDir    string `yaml:"config_dir"`
	ConfigDirEnv string `yaml:"config_dir_env"`
	HostEnv      string `yaml:"host_env"`
	CommandPath  string `yaml:"command_path"`
}

type AuthInfo struct {
	Validate *AuthValidate `yaml:"validate,omitempty"`
	Login    *AuthLogin    `yaml:"login,omitempty"`
}

type UpdateInfo struct {
	GitHub *GitHubUpdate `yaml:"github,omitempty"`
}

type AuthLogin struct {
	Type        string `yaml:"type"`
	StartPath   string `yaml:"start_path"`
	TokenPath   string `yaml:"token_path"`
	RefreshPath string `yaml:"refresh_path,omitempty"`
}

type GitHubUpdate struct {
	Owner string `yaml:"owner"`
	Repo  string `yaml:"repo"`
	Asset string `yaml:"asset"`
}

type AuthValidate struct {
	Method  string              `yaml:"method"`
	Path    string              `yaml:"path"`
	Display AuthValidateDisplay `yaml:"display"`
}

type AuthValidateDisplay struct {
	UsernameField string `yaml:"username_field"`
	FallbackField string `yaml:"fallback_field"`
}

const (
	CommandPathAuto       = "auto"
	CommandPathFlat       = "flat"
	CommandPathNamespaced = "namespaced"
	AuthLoginOAuthDevice  = "oauth_device"
)

// Load parses raw cli.yaml bytes into a Manifest. The caller (typically main.go)
// supplies the bytes — usually via //go:embed at the module root — so that
// pkg/config stays free of a reverse import on the downstream repo root.
//
// Empty identity fields are filled from cli.name: config_dir defaults to the
// name itself (→ ~/.config/<name>/), and the env var names default to
// <NAME>_CONFIG_DIR / <NAME>_HOST. Downstreams may pin explicit values to
// preserve historical env vars across a rename.
func Load(bytes []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(bytes, &m); err != nil {
		return nil, fmt.Errorf("parse cli.yaml: %w", err)
	}
	if m.CLI.Name == "" {
		return nil, fmt.Errorf("cli.name is required")
	}
	if m.Update.GitHub != nil {
		m.Update.GitHub.Owner = strings.TrimSpace(m.Update.GitHub.Owner)
		m.Update.GitHub.Repo = strings.TrimSpace(m.Update.GitHub.Repo)
		m.Update.GitHub.Asset = strings.TrimSpace(m.Update.GitHub.Asset)
		if m.Update.GitHub.Owner == "" || m.Update.GitHub.Repo == "" || m.Update.GitHub.Asset == "" {
			return nil, fmt.Errorf("update.github.owner, update.github.repo, and update.github.asset are required")
		}
	}
	if m.Auth.Login != nil {
		m.Auth.Login.Type = strings.ToLower(strings.TrimSpace(m.Auth.Login.Type))
		m.Auth.Login.StartPath = strings.TrimSpace(m.Auth.Login.StartPath)
		m.Auth.Login.TokenPath = strings.TrimSpace(m.Auth.Login.TokenPath)
		m.Auth.Login.RefreshPath = strings.TrimSpace(m.Auth.Login.RefreshPath)
		if m.Auth.Login.Type != AuthLoginOAuthDevice {
			return nil, fmt.Errorf("auth.login.type must be %q", AuthLoginOAuthDevice)
		}
		if m.Auth.Login.StartPath == "" || m.Auth.Login.TokenPath == "" {
			return nil, fmt.Errorf("auth.login.start_path and auth.login.token_path are required")
		}
		if !strings.HasPrefix(m.Auth.Login.StartPath, "/") || !strings.HasPrefix(m.Auth.Login.TokenPath, "/") {
			return nil, fmt.Errorf("auth.login.start_path and auth.login.token_path must start with /")
		}
		if m.Auth.Login.RefreshPath != "" && !strings.HasPrefix(m.Auth.Login.RefreshPath, "/") {
			return nil, fmt.Errorf("auth.login.refresh_path must start with /")
		}
	}
	m.CLI.CommandPath = strings.ToLower(strings.TrimSpace(m.CLI.CommandPath))
	if m.CLI.CommandPath == "" {
		m.CLI.CommandPath = CommandPathAuto
	}
	switch m.CLI.CommandPath {
	case CommandPathAuto, CommandPathFlat, CommandPathNamespaced:
	default:
		return nil, fmt.Errorf("cli.command_path must be one of %q, %q, or %q", CommandPathAuto, CommandPathFlat, CommandPathNamespaced)
	}
	upper := strings.ToUpper(m.CLI.Name)
	if m.CLI.ConfigDir == "" {
		m.CLI.ConfigDir = m.CLI.Name
	}
	if m.CLI.ConfigDirEnv == "" {
		m.CLI.ConfigDirEnv = upper + "_CONFIG_DIR"
	}
	if m.CLI.HostEnv == "" {
		m.CLI.HostEnv = upper + "_HOST"
	}
	return &m, nil
}

var (
	boundMu sync.RWMutex
	bound   *Manifest
)

// Bind stores the manifest for retrieval by package-level helpers (hosts.go
// configDir, runtime error renderers). main.go calls it once after Load.
// Tests may call it repeatedly with synthetic manifests.
func Bind(m *Manifest) {
	boundMu.Lock()
	bound = m
	boundMu.Unlock()
}

// Active returns the manifest previously passed to Bind. An unbound read is
// a programmer error and panics rather than silently falling back to any
// particular CLI identity.
func Active() *Manifest {
	boundMu.RLock()
	defer boundMu.RUnlock()
	if bound == nil {
		panic("config: Active() called before Bind()")
	}
	return bound
}
