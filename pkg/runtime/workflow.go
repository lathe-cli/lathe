package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
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
				return UsageError(cmd, WithUsageDetail(err, requiredFlagsDetail(cmd)))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			if _, ok := formatters[format]; !ok {
				return UsageError(cmd, WithUsageDetail(fmt.Errorf("unsupported output format"), outputFormatDetail()))
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

		host, clientOpts, err := operationHostOptions(cmd, step.Operation, false)
		if err != nil {
			return fail(err)
		}
		reporter.noticeImplicitHost(cmd.ErrOrStderr(), host)

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
	value, err := evalWorkflowValue(spec.OutputFrom, state, workflowOutput)
	if err != nil {
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
