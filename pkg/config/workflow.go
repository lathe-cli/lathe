package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

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
	ID     string              `yaml:"id"`
	Uses   string              `yaml:"uses"`
	When   []WorkflowCondition `yaml:"when,omitempty"`
	Params map[string]string   `yaml:"params,omitempty"`
	Set    map[string]string   `yaml:"set,omitempty"`
	SetStr map[string]string   `yaml:"set_str,omitempty"`
}

type WorkflowCondition struct {
	Value    string                  `yaml:"value"`
	Operator string                  `yaml:"operator"`
	Values   WorkflowConditionValues `yaml:"values"`
}
type WorkflowConditionValues []string

func (v *WorkflowConditionValues) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("workflow condition values must be a list")
	}
	out := make(WorkflowConditionValues, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("workflow condition values must contain scalars")
		}
		out = append(out, normalizeWorkflowConditionValue(item))
	}
	*v = out
	return nil
}

func normalizeWorkflowConditionValue(node *yaml.Node) string {
	switch node.Tag {
	case "!!float":
		var f float64
		if err := node.Decode(&f); err == nil {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	case "!!int":
		if i, err := strconv.ParseInt(node.Value, 0, 64); err == nil {
			return strconv.FormatInt(i, 10)
		}
	case "!!bool":
		if b, err := strconv.ParseBool(node.Value); err == nil {
			return strconv.FormatBool(b)
		}
	case "!!null":
		return ""
	}
	return node.Value
}

type WorkflowOutput struct {
	From              string   `yaml:"from,omitempty"`
	ListPath          string   `yaml:"list_path,omitempty"`
	DefaultColumns    []string `yaml:"default_columns,omitempty"`
	ResponseMediaType string   `yaml:"response_media_type,omitempty"`
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
			for k := range step.When {
				cond := &step.When[k]
				cond.Operator = strings.ToLower(strings.TrimSpace(cond.Operator))
				if strings.TrimSpace(cond.Value) == "" {
					return fmt.Errorf("workflow command %q step %q when[%d].value is required", cmd.Use, step.ID, k)
				}
				if !validWorkflowOperator(cond.Operator) {
					return fmt.Errorf("workflow command %q step %q when[%d].operator must be %q or %q", cmd.Use, step.ID, k, "in", "notin")
				}
				if len(cond.Values) == 0 {
					return fmt.Errorf("workflow command %q step %q when[%d].values must not be empty", cmd.Use, step.ID, k)
				}
			}
		}
	}
	return nil
}

func workflowInputFlag(name string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(name)
}

func validWorkflowOperator(value string) bool {
	return value == "in" || value == "notin"
}

func validWorkflowInputType(value string) bool {
	switch value {
	case "string", "int64", "float64", "bool", "[]string", "[]int64", "[]float64", "[]bool":
		return true
	default:
		return false
	}
}
