package config

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	CLI      CLIInfo      `yaml:"cli"`
	Auth     AuthInfo     `yaml:"auth"`
	Update   UpdateInfo   `yaml:"update,omitempty"`
	Skill    SkillInfo    `yaml:"skill,omitempty"`
	Workflow WorkflowInfo `yaml:"workflow,omitempty"`
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

type SkillInfo struct {
	Bundle bool `yaml:"bundle,omitempty"`
}

type WorkflowInfo struct {
	Version  int               `yaml:"version,omitempty"`
	Commands []WorkflowCommand `yaml:"commands,omitempty"`
}

type WorkflowCommand struct {
	Use        string          `yaml:"use"`
	Aliases    []string        `yaml:"aliases,omitempty"`
	Short      string          `yaml:"short,omitempty"`
	Long       string          `yaml:"long,omitempty"`
	Example    string          `yaml:"example,omitempty"`
	Hidden     bool            `yaml:"hidden,omitempty"`
	Deprecated bool            `yaml:"deprecated,omitempty"`
	Inputs     []WorkflowInput `yaml:"inputs,omitempty"`
	Steps      []WorkflowStep  `yaml:"steps,omitempty"`
	Output     WorkflowOutput  `yaml:"output,omitempty"`
}

type WorkflowInput struct {
	Name       string   `yaml:"name"`
	Flag       string   `yaml:"flag,omitempty"`
	Type       string   `yaml:"type,omitempty"`
	Help       string   `yaml:"help,omitempty"`
	Required   bool     `yaml:"required,omitempty"`
	Default    string   `yaml:"default,omitempty"`
	Enum       []string `yaml:"enum,omitempty"`
	Format     string   `yaml:"format,omitempty"`
	Deprecated bool     `yaml:"deprecated,omitempty"`
}

type WorkflowStep struct {
	ID     string            `yaml:"id"`
	Uses   string            `yaml:"uses"`
	Params map[string]string `yaml:"params,omitempty"`
	Set    map[string]string `yaml:"set,omitempty"`
	SetStr map[string]string `yaml:"set_str,omitempty"`
}

type WorkflowOutput struct {
	From              string   `yaml:"from,omitempty"`
	ListPath          string   `yaml:"list_path,omitempty"`
	DefaultColumns    []string `yaml:"default_columns,omitempty"`
	ResponseMediaType string   `yaml:"response_media_type,omitempty"`
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
	Assert  *AuthValidateAssert `yaml:"assert,omitempty"`
}

type AuthValidateDisplay struct {
	UsernameField string `yaml:"username_field"`
	FallbackField string `yaml:"fallback_field"`
}

type AuthValidateAssert struct {
	Field    string `yaml:"field,omitempty"`
	NonEmpty bool   `yaml:"non_empty,omitempty"`
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
	if m.Auth.Validate != nil && m.Auth.Validate.Assert != nil {
		m.Auth.Validate.Assert.Field = strings.TrimSpace(m.Auth.Validate.Assert.Field)
		if m.Auth.Validate.Assert.Field == "" && !m.Auth.Validate.Assert.NonEmpty {
			return nil, fmt.Errorf("auth.validate.assert requires field or non_empty")
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

func normalizeWorkflow(workflow *WorkflowInfo) error {
	if len(workflow.Commands) == 0 {
		workflow.Version = 0
		return nil
	}
	if workflow.Version == 0 {
		workflow.Version = 1
	}
	if workflow.Version != 1 {
		return fmt.Errorf("workflow.version must be 1")
	}
	seen := map[string]bool{}
	for i := range workflow.Commands {
		cmd := &workflow.Commands[i]
		cmd.Use = strings.TrimSpace(cmd.Use)
		if cmd.Use == "" || len(strings.Fields(cmd.Use)) != 1 {
			return fmt.Errorf("workflow.commands[%d].use must be a single command name", i)
		}
		if seen[cmd.Use] {
			return fmt.Errorf("workflow command %q is declared more than once", cmd.Use)
		}
		seen[cmd.Use] = true
		inputNames := map[string]bool{}
		inputFlags := map[string]bool{}
		for j := range cmd.Inputs {
			input := &cmd.Inputs[j]
			input.Name = strings.TrimSpace(input.Name)
			input.Flag = strings.TrimSpace(input.Flag)
			input.Type = strings.TrimSpace(input.Type)
			if input.Name == "" {
				return fmt.Errorf("workflow command %q input %d name is required", cmd.Use, j)
			}
			if input.Flag == "" {
				input.Flag = workflowInputFlag(input.Name)
			}
			if input.Type == "" {
				input.Type = "string"
			}
			if !validWorkflowInputType(input.Type) {
				return fmt.Errorf("workflow command %q input %q type %q is not supported", cmd.Use, input.Name, input.Type)
			}
			if inputNames[input.Name] {
				return fmt.Errorf("workflow command %q input name %q is declared more than once", cmd.Use, input.Name)
			}
			if inputFlags[input.Flag] {
				return fmt.Errorf("workflow command %q input flag %q is declared more than once", cmd.Use, input.Flag)
			}
			inputNames[input.Name] = true
			inputFlags[input.Flag] = true
		}
		if len(cmd.Steps) == 0 {
			return fmt.Errorf("workflow command %q must have at least one step", cmd.Use)
		}
		stepIDs := map[string]bool{}
		for j := range cmd.Steps {
			step := &cmd.Steps[j]
			step.ID = strings.TrimSpace(step.ID)
			step.Uses = strings.TrimSpace(step.Uses)
			if step.ID == "" {
				return fmt.Errorf("workflow command %q steps[%d].id is required", cmd.Use, j)
			}
			if strings.Contains(step.ID, ".") {
				return fmt.Errorf("workflow command %q step id %q must not contain dots", cmd.Use, step.ID)
			}
			if stepIDs[step.ID] {
				return fmt.Errorf("workflow command %q step %q is declared more than once", cmd.Use, step.ID)
			}
			stepIDs[step.ID] = true
			if step.Uses == "" {
				return fmt.Errorf("workflow command %q step %q uses is required", cmd.Use, step.ID)
			}
		}
	}
	return nil
}

func workflowInputFlag(name string) string {
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	return name
}

func validWorkflowInputType(value string) bool {
	switch value {
	case "string", "int64", "float64", "bool", "[]string", "[]int64", "[]float64", "[]bool":
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
