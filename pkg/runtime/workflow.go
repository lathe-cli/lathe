package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type WorkflowResult struct {
	Status string               `json:"status"`
	Steps  []WorkflowStepResult `json:"steps"`
}

type WorkflowStepResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type WorkflowError struct {
	StepID string
	Err    error
	Result WorkflowResult
}

func (e *WorkflowError) Error() string {
	return fmt.Sprintf("workflow step %q failed: %v", e.StepID, e.Err)
}

func (e *WorkflowError) Unwrap() error {
	return e.Err
}

func BuildWorkflows(root *cobra.Command, specs []WorkflowSpec) error {
	for _, spec := range specs {
		if findChildCommand(root, spec.Use) != nil {
			return fmt.Errorf("workflow command %q conflicts with existing root command", spec.Use)
		}
		for _, alias := range spec.Aliases {
			if findChildCommand(root, alias) != nil {
				return fmt.Errorf("workflow command %q alias %q conflicts with existing root command", spec.Use, alias)
			}
		}
	}
	if len(specs) > 0 {
		AttachCapability(root, CapabilityWorkflowDSL)
	}
	for _, spec := range specs {
		cmd := buildWorkflowCmd(spec)
		AttachCatalogWorkflowCommand(cmd, spec)
		root.AddCommand(cmd)
	}
	return nil
}

func buildWorkflowCmd(spec WorkflowSpec) *cobra.Command {
	vals := make(map[string]any, len(spec.Params))
	cmd := &cobra.Command{
		Use:     spec.Use,
		Aliases: spec.Aliases,
		Short:   spec.Short,
		Long:    spec.Long,
		Example: spec.Example,
		Hidden:  spec.Hidden,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := resolveSafeInputFlags(cmd, spec.Params, vals); err != nil {
				return err
			}
			if err := validateRequiredSafeParams(cmd, spec.Params, false); err != nil {
				return err
			}
			if err := validateOperationEnums(CommandSpec{Params: spec.Params}, OperationInput{
				Values:  vals,
				Changed: operationChangedFlags(cmd, spec.Params),
			}); err != nil {
				return err
			}
			result, data, err := executeWorkflow(cmd, spec, vals)
			if err != nil {
				return err
			}
			if data == nil {
				var marshalErr error
				data, marshalErr = json.Marshal(result)
				if marshalErr != nil {
					return marshalErr
				}
			}
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			return FormatOutput(data, format, cmd.OutOrStdout(), spec.Output)
		},
	}
	for _, p := range spec.Params {
		bindParamFlag(cmd, vals, p, false)
	}
	if spec.Deprecated {
		cmd.Deprecated = "this command is deprecated"
	}
	return cmd
}

func executeWorkflow(cmd *cobra.Command, spec WorkflowSpec, vals map[string]any) (WorkflowResult, []byte, error) {
	state := workflowState{
		inputs: workflowInputValues(cmd, spec, vals),
		steps:  map[string]any{},
	}
	result := WorkflowResult{Status: "ok", Steps: make([]WorkflowStepResult, 0, len(spec.Steps))}
	for _, step := range spec.Steps {
		stepResult := WorkflowStepResult{ID: step.ID, Status: "ok"}
		var hostname string
		var clientOpts ClientOptions
		var err error
		if step.Operation.Security != nil && step.Operation.Security.Public {
			hostname, clientOpts, err = tryLoadHostOptionsMaybeRefresh(cmd, step.Operation.DefaultHostname, true)
		} else {
			hostname, clientOpts, err = loadHostOptionsMaybeRefresh(cmd, step.Operation.DefaultHostname, true)
		}
		if err != nil {
			stepResult.Status = "failed"
			result.Status = "failed"
			result.Steps = append(result.Steps, stepResult)
			return result, nil, &WorkflowError{StepID: step.ID, Err: err, Result: result}
		}
		if v, err := cmd.Root().PersistentFlags().GetBool("debug"); err == nil && v {
			clientOpts.Debug = true
		}
		clientOpts.UserAgent = cmd.Root().Use
		input, err := workflowOperationInput(step, state)
		if err != nil {
			stepResult.Status = "failed"
			result.Status = "failed"
			result.Steps = append(result.Steps, stepResult)
			return result, nil, &WorkflowError{StepID: step.ID, Err: err, Result: result}
		}
		opResult, err := InvokeOperation(cmd.Context(), step.Operation, input, OperationOptions{
			Hostname: hostname,
			Client:   clientOpts,
		})
		if err != nil {
			stepResult.Status = "failed"
			result.Status = "failed"
			result.Steps = append(result.Steps, stepResult)
			return result, nil, &WorkflowError{StepID: step.ID, Err: err, Result: result}
		}
		state.steps[step.ID] = workflowStepValue(opResult.Data)
		result.Steps = append(result.Steps, stepResult)
	}
	if strings.TrimSpace(spec.OutputFrom) == "" {
		return result, nil, nil
	}
	value, err := evalWorkflowValue(spec.OutputFrom, state)
	if err != nil {
		return result, nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return result, nil, err
	}
	return result, data, nil
}

type workflowState struct {
	inputs map[string]any
	steps  map[string]any
}

func workflowInputValues(cmd *cobra.Command, spec WorkflowSpec, vals map[string]any) map[string]any {
	input := OperationInput{Values: vals, Changed: operationChangedFlags(cmd, spec.Params)}
	out := make(map[string]any, len(spec.Params))
	for _, p := range spec.Params {
		if !operationChanged(input, p) {
			continue
		}
		v, ok, err := operationValue(input, p)
		if err != nil || !ok {
			continue
		}
		out[p.Name] = v
		out[p.Flag] = v
	}
	return out
}

func workflowOperationInput(step WorkflowStepSpec, state workflowState) (OperationInput, error) {
	values := make(map[string]any, len(step.Params))
	for key, expr := range step.Params {
		value, err := evalWorkflowValue(expr, state)
		if err != nil {
			return OperationInput{}, fmt.Errorf("step %s param %s: %w", step.ID, key, err)
		}
		values[key] = value
	}
	sets, err := evalWorkflowAssignments(step.BodySets, state)
	if err != nil {
		return OperationInput{}, fmt.Errorf("step %s body set: %w", step.ID, err)
	}
	stringSets, err := evalWorkflowAssignments(step.BodyStringSets, state)
	if err != nil {
		return OperationInput{}, fmt.Errorf("step %s body set-str: %w", step.ID, err)
	}
	return OperationInput{
		Values:         values,
		BodySets:       sets,
		BodyStringSets: stringSets,
	}, nil
}

func evalWorkflowAssignments(values []WorkflowValue, state workflowState) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		evaluated, err := evalWorkflowString(value.Value, state)
		if err != nil {
			return nil, err
		}
		out = append(out, value.Name+"="+evaluated)
	}
	return out, nil
}

func workflowStepValue(data []byte) any {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err == nil {
		return value
	}
	return string(data)
}

func evalWorkflowString(expr string, state workflowState) (string, error) {
	if !strings.Contains(expr, "${") {
		return expr, nil
	}
	var out strings.Builder
	rest := expr
	for {
		start := strings.Index(rest, "${")
		if start < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:start])
		after := rest[start+2:]
		end := strings.Index(after, "}")
		if end < 0 {
			return "", fmt.Errorf("unterminated reference in %q", expr)
		}
		ref := strings.TrimSpace(after[:end])
		value, err := workflowRefValue(ref, state)
		if err != nil {
			return "", err
		}
		out.WriteString(workflowString(value))
		rest = after[end+1:]
	}
}

func evalWorkflowValue(expr string, state workflowState) (any, error) {
	trimmed := strings.TrimSpace(expr)
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") && strings.Count(trimmed, "${") == 1 {
		return workflowRefValue(strings.TrimSpace(trimmed[2:len(trimmed)-1]), state)
	}
	return evalWorkflowString(expr, state)
}

func workflowRefValue(ref string, state workflowState) (any, error) {
	if name, ok := strings.CutPrefix(ref, "input."); ok {
		value, exists := state.inputs[name]
		if !exists {
			return nil, fmt.Errorf("unknown input %q", name)
		}
		return value, nil
	}
	if rest, ok := strings.CutPrefix(ref, "steps."); ok {
		id, path, _ := strings.Cut(rest, ".")
		step, exists := state.steps[id]
		if !exists {
			return nil, fmt.Errorf("unknown step %q", id)
		}
		if path == "" {
			return step, nil
		}
		value, exists := getNestedPath(step, path)
		if !exists {
			return nil, fmt.Errorf("step %q has no output path %q", id, path)
		}
		return value, nil
	}
	return nil, fmt.Errorf("unknown reference %q", ref)
}

func workflowString(value any) string {
	switch tv := value.(type) {
	case nil:
		return ""
	case string:
		return tv
	case []byte:
		return string(tv)
	case json.Number:
		return tv.String()
	case bool:
		return fmt.Sprint(tv)
	case float64:
		return strconv.FormatFloat(tv, 'f', -1, 64)
	default:
		data, err := json.Marshal(tv)
		if err == nil {
			return string(data)
		}
		return fmt.Sprint(tv)
	}
}
