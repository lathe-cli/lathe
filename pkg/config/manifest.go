package config

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	CLI  CLIInfo  `yaml:"cli"`
	Auth AuthInfo `yaml:"auth"`
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
	Validate     *AuthValidate `yaml:"validate,omitempty"`
	LoginAliases []string      `yaml:"login_aliases,omitempty"`
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
	aliases, err := normalizeAuthLoginAliases(m.Auth.LoginAliases)
	if err != nil {
		return nil, err
	}
	m.Auth.LoginAliases = aliases
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

func normalizeAuthLoginAliases(aliases []string) ([]string, error) {
	if len(aliases) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(aliases))
	seen := map[string]bool{}
	for _, raw := range aliases {
		alias := strings.ToLower(strings.TrimSpace(raw))
		if alias == "" {
			return nil, fmt.Errorf("auth.login_aliases cannot contain empty aliases")
		}
		if len(strings.Fields(alias)) != 1 {
			return nil, fmt.Errorf("auth.login_aliases entries must be single command names: %q", raw)
		}
		if reservedAuthLoginAlias(alias) {
			return nil, fmt.Errorf("auth.login_aliases cannot use reserved root command %q", alias)
		}
		if seen[alias] {
			return nil, fmt.Errorf("auth.login_aliases contains duplicate alias %q", alias)
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out, nil
}

func reservedAuthLoginAlias(alias string) bool {
	switch alias {
	case "auth", "commands", "completion", "help", "search", "version":
		return true
	default:
		return false
	}
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
