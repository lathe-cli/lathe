package config

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	CLI      CLIInfo                `yaml:"cli"`
	Auth     AuthInfo               `yaml:"auth"`
	Contexts map[string]ContextInfo `yaml:"contexts,omitempty"`
	Update   UpdateInfo             `yaml:"update,omitempty"`
	Skill    SkillInfo              `yaml:"skill,omitempty"`
	Workflow WorkflowInfo           `yaml:"workflow,omitempty"`
}

type ContextInfo struct {
	Env      string `yaml:"env,omitempty"`
	LocalSet bool   `yaml:"local_set,omitempty"`
}

type CLIInfo struct {
	Name         string `yaml:"name"`
	Short        string `yaml:"short"`
	ConfigDir    string `yaml:"config_dir"`
	ConfigDirEnv string `yaml:"config_dir_env"`
	HostEnv      string `yaml:"host_env"`
	CommandPath  string `yaml:"command_path"`
}

type UpdateInfo struct {
	GitHub *GitHubUpdate `yaml:"github,omitempty"`
}

type SkillInfo struct {
	Bundle bool `yaml:"bundle,omitempty"`
}

type GitHubUpdate struct {
	Owner string `yaml:"owner"`
	Repo  string `yaml:"repo"`
	Asset string `yaml:"asset"`
}

const (
	CommandPathAuto       = "auto"
	CommandPathFlat       = "flat"
	CommandPathNamespaced = "namespaced"
)

func Load(bytes []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(bytes, &m); err != nil {
		return nil, fmt.Errorf("parse cli.yaml: %w", err)
	}
	if m.CLI.Name == "" {
		return nil, fmt.Errorf("cli.name is required")
	}
	if err := normalizeContexts(m.Contexts); err != nil {
		return nil, err
	}
	if err := normalizeWorkflow(&m.Workflow); err != nil {
		return nil, err
	}
	if m.Update.GitHub != nil {
		m.Update.GitHub.Owner = strings.TrimSpace(m.Update.GitHub.Owner)
		m.Update.GitHub.Repo = strings.TrimSpace(m.Update.GitHub.Repo)
		m.Update.GitHub.Asset = strings.TrimSpace(m.Update.GitHub.Asset)
		if m.Update.GitHub.Owner == "" || m.Update.GitHub.Repo == "" || m.Update.GitHub.Asset == "" {
			return nil, fmt.Errorf("update.github.owner, update.github.repo, and update.github.asset are required")
		}
	}
	if err := normalizeAuth(&m.Auth, m.Contexts); err != nil {
		return nil, err
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
	for name, info := range m.Contexts {
		if info.Env != "" && (strings.EqualFold(info.Env, m.CLI.HostEnv) || strings.EqualFold(info.Env, m.CLI.ConfigDirEnv)) {
			return nil, fmt.Errorf("contexts.%s.env %q is reserved by CLI configuration", name, info.Env)
		}
	}
	return &m, nil
}

func normalizeContexts(contexts map[string]ContextInfo) error {
	seenEnv := map[string]string{}
	for name, info := range contexts {
		if !contextNamePattern.MatchString(name) {
			return fmt.Errorf("context name %q must contain only letters, digits, hyphens, or underscores and start with a letter", name)
		}
		info.Env = strings.TrimSpace(info.Env)
		if info.Env != "" {
			if !envNamePattern.MatchString(info.Env) {
				return fmt.Errorf("contexts.%s.env %q is not a valid environment variable name", name, info.Env)
			}
			envKey := strings.ToUpper(info.Env)
			if prior := seenEnv[envKey]; prior != "" {
				return fmt.Errorf("contexts %q and %q use the same environment variable %s", prior, name, info.Env)
			}
			seenEnv[envKey] = name
		}
		contexts[name] = info
	}
	return nil
}

var contextNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	boundMu sync.RWMutex
	bound   *Manifest
)

func Bind(m *Manifest) {
	boundMu.Lock()
	bound = m
	boundMu.Unlock()
}

func Active() *Manifest {
	boundMu.RLock()
	defer boundMu.RUnlock()
	if bound == nil {
		panic("config: Active() called before Bind()")
	}
	return bound
}
