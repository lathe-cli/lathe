package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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
		if findChildCommand(root, spec.Use) != nil || spec.Use == completionRootName {
			return fmt.Errorf("workflow command %q conflicts with existing root command", spec.Use)
		}
		for _, alias := range spec.Aliases {
			if findChildCommand(root, alias) != nil || alias == completionRootName {
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
		Args:    UsageArgs(cobra.NoArgs),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.ValidateRequiredFlags(); err != nil {
				return UsageError(cmd, err)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			if _, ok := formatters[format]; !ok {
				return UsageError(cmd, fmt.Errorf("unsupported output format"))
			}
			if err := resolveSafeInputFlags(cmd, spec.Params, vals); err != nil {
				return UsageError(cmd, err)
			}
			changed := operationChangedFlags(cmd, spec.Params)
			if err := validateRequiredParams(spec.Params, false, changed); err != nil {
				return UsageError(cmd, err)
			}
			if err := validateOperationEnums(CommandSpec{Params: spec.Params}, OperationInput{
				Values:  vals,
				Changed: changed,
			}); err != nil {
				return UsageError(cmd, err)
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
		inputs:  workflowInputValues(cmd, spec, vals),
		steps:   map[string]any{},
		skipped: map[string]bool{},
	}
	result := WorkflowResult{Status: "ok", Steps: make([]WorkflowStepResult, 0, len(spec.Steps))}
	var reporter hostReporter
	for _, step := range spec.Steps {
		stepResult := WorkflowStepResult{ID: step.ID, Status: "ok"}
		fail := func(err error) (WorkflowResult, []byte, error) {
			stepResult.Status = "failed"
			result.Status = "failed"
			result.Steps = append(result.Steps, stepResult)
			return result, nil, &WorkflowError{StepID: step.ID, Err: err, Result: result}
		}
		skip := func() {
			state.skipped[step.ID] = true
			stepResult.Status = "skipped"
			result.Steps = append(result.Steps, stepResult)
		}

		// Conditions are evaluated before any host or auth work so a skipped
		// step never loads credentials or triggers a token refresh.
		run, err := evalWorkflowConditions(step.When, state)
		if err != nil {
			if errors.Is(err, errStepSkipped) {
				skip()
				continue
			}
			return fail(err)
		}
		if !run {
			skip()
			continue
		}

		input, err := workflowOperationInput(step, state)
		if err != nil {
			if errors.Is(err, errStepSkipped) {
				skip()
				continue
			}
			return fail(err)
		}
		if err := resolveCommandContexts(cmd, step.Operation, &input); err != nil {
			return fail(err)
		}
		if err := validateOperationInput(step.Operation, input); err != nil {
			return fail(UsageError(cmd, err))
		}

		var host HostResolution
		var clientOpts ClientOptions
		if step.Operation.Security != nil && step.Operation.Security.Public {
			host, clientOpts, err = tryLoadHostOptions(cmd, step.Operation.DefaultHostname, true)
		} else {
			host, clientOpts, err = loadHostOptions(cmd, step.Operation.DefaultHostname, true)
		}
		if err != nil {
			return fail(err)
		}
		reporter.noticeImplicitHost(cmd.ErrOrStderr(), host)
		if v, err := cmd.Root().PersistentFlags().GetBool("debug"); err == nil && v {
			clientOpts.Debug = true
		}
		clientOpts.UserAgent = cmd.Root().Use

		opResult, err := InvokeOperation(cmd.Context(), step.Operation, input, OperationOptions{
			Hostname:   host.Hostname,
			HostSource: host.Source,
			Client:     clientOpts,
		})
		if err != nil {
			return fail(err)
		}
		state.steps[step.ID] = workflowStepValue(opResult.Data)
		if opResult.Outcome == OperationOutcomePaused {
			stepResult.Status = OperationOutcomePaused
			result.Status = OperationOutcomePaused
			result.Steps = append(result.Steps, stepResult)
			return result, opResult.Data, nil
		}
		result.Steps = append(result.Steps, stepResult)
	}
	if strings.TrimSpace(spec.OutputFrom) == "" {
		return result, nil, nil
	}
	value, err := evalWorkflowOutputValue(spec.OutputFrom, state)
	if err != nil {
		// A bare reference to a skipped step degrades to the step summary
		// rather than failing the command.
		if errors.Is(err, errStepSkipped) {
			return result, nil, nil
		}
		return result, nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return result, nil, err
	}
	return result, data, nil
}

type workflowState struct {
	inputs  map[string]any
	steps   map[string]any
	skipped map[string]bool
}

// errStepSkipped marks a reference to a step that was skipped. It propagates
// out of reference evaluation so the referencing step is skipped in turn, which
// makes propagation transitive without a dependency graph.
var errStepSkipped = errors.New("workflow step was skipped")

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

// evalWorkflowConditions reports whether a step should run. Conditions are
// joined with AND; values within one condition are joined with OR.
func evalWorkflowConditions(conditions []WorkflowCondition, state workflowState) (bool, error) {
	for _, cond := range conditions {
		actual, err := evalWorkflowConditionValue(cond.Value, state)
		if err != nil {
			return false, err
		}
		matched := slices.Contains(cond.Values, actual)
		if cond.Operator == "notin" {
			matched = !matched
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

// evalWorkflowConditionValue is the lenient evaluator, kept separate from
// evalWorkflowValue rather than wrapping it. Leniency applies per reference:
// a reference that does not resolve contributes the empty string while the
// surrounding literal text is preserved, so "prefix-${steps.probe.missing}"
// evaluates to "prefix-". Collapsing the whole expression would silently turn
// a partially resolvable condition into a comparison against "".
//
// Leniency stops at skipped steps: that sentinel propagates so the referencing
// step is skipped rather than compared against an empty value.
func evalWorkflowConditionValue(expr string, state workflowState) (string, error) {
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
			// Unterminated references are rejected at codegen time. Treat the
			// remainder as literal text instead of failing the condition.
			out.WriteString(rest[start:])
			return out.String(), nil
		}
		value, err := workflowRefValue(strings.TrimSpace(after[:end]), state)
		switch {
		case err == nil:
			out.WriteString(workflowString(value))
		case errors.Is(err, errStepSkipped):
			return "", err
		}
		rest = after[end+1:]
	}
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err == nil {
		var trailing any
		if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
			return value
		}
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

// evalWorkflowOutputValue evaluates output.from expressions. A bare reference
// to a skipped step still propagates errStepSkipped (the caller degrades to the
// step summary). A composite expression substitutes null for skipped references
// so the remaining step data survives.
func evalWorkflowOutputValue(expr string, state workflowState) (any, error) {
	trimmed := strings.TrimSpace(expr)
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") && strings.Count(trimmed, "${") == 1 {
		return workflowRefValue(strings.TrimSpace(trimmed[2:len(trimmed)-1]), state)
	}
	return evalWorkflowOutputString(expr, state)
}

// evalWorkflowOutputString is like evalWorkflowString but substitutes "null"
// for references to skipped steps instead of propagating errStepSkipped. This
// lets a JSON-literal output.from like '{"a":${steps.x},"b":${steps.y}}'
// produce '{"a":<data>,"b":null}' when y is skipped, rather than discarding
// the entire aggregate.
func evalWorkflowOutputString(expr string, state workflowState) (string, error) {
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
			if errors.Is(err, errStepSkipped) {
				out.WriteString("null")
				rest = after[end+1:]
				continue
			}
			return "", err
		}
		out.WriteString(workflowString(value))
		rest = after[end+1:]
	}
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
		if state.skipped[id] {
			return nil, fmt.Errorf("step %q: %w", id, errStepSkipped)
		}
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
		raw := tv.String()
		if strings.ContainsAny(raw, ".eE") {
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				return strconv.FormatFloat(f, 'f', -1, 64)
			}
		} else if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return strconv.FormatInt(i, 10)
		}
		return raw
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
