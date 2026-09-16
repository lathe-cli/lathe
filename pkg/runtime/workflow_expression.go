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

type workflowState struct {
	inputs  map[string]any
	steps   map[string]any
	skipped map[string]bool
}

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

func evalWorkflowConditions(conditions []WorkflowCondition, state workflowState) (bool, error) {
	for _, cond := range conditions {
		actual, err := evalWorkflowString(cond.Value, state, workflowCondition)
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

func workflowOperationInput(step WorkflowStepSpec, state workflowState) (OperationInput, error) {
	values := make(map[string]any, len(step.Params))
	for key, expr := range step.Params {
		value, err := evalWorkflowValue(expr, state, workflowStrict)
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
		evaluated, err := evalWorkflowString(value.Value, state, workflowStrict)
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

type workflowExpressionMode int

const (
	workflowStrict workflowExpressionMode = iota
	workflowCondition
	workflowOutput
)

func evalWorkflowString(expr string, state workflowState, mode workflowExpressionMode) (string, error) {
	var out strings.Builder
	for rest := expr; ; {
		before, after, found := strings.Cut(rest, "${")
		out.WriteString(before)
		if !found {
			return out.String(), nil
		}
		ref, remaining, closed := strings.Cut(after, "}")
		if !closed {
			if mode == workflowCondition {
				out.WriteString("${" + after)
				return out.String(), nil
			}
			return "", fmt.Errorf("unterminated reference in %q", expr)
		}
		value, err := workflowRefValue(strings.TrimSpace(ref), state)
		switch {
		case err == nil:
			out.WriteString(workflowString(value))
		case mode == workflowOutput && errors.Is(err, errStepSkipped):
			out.WriteString("null")
		case mode == workflowCondition && !errors.Is(err, errStepSkipped):
		default:
			return "", err
		}
		rest = remaining
	}
}

func evalWorkflowValue(expr string, state workflowState, mode workflowExpressionMode) (any, error) {
	trimmed := strings.TrimSpace(expr)
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") && strings.Count(trimmed, "${") == 1 {
		return workflowRefValue(strings.TrimSpace(trimmed[2:len(trimmed)-1]), state)
	}
	return evalWorkflowString(expr, state, mode)
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
