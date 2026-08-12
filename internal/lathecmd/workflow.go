package lathecmd

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/app"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func buildWorkflowSpecs(manifest *config.Manifest, modules []app.Module, shortcutRootNames []string) ([]runtime.WorkflowSpec, error) {
	if len(manifest.Workflow.Commands) == 0 {
		return nil, nil
	}
	lookup := workflowOperationLookup(modules)
	rootNames := workflowRootNames(modules, shortcutRootNames)
	out := make([]runtime.WorkflowSpec, 0, len(manifest.Workflow.Commands))
	for _, command := range manifest.Workflow.Commands {
		if rootNames[workflowLookupKey(command.Use)] {
			return nil, fmt.Errorf("workflow command %q conflicts with an existing generated root command", command.Use)
		}
		for _, alias := range command.Aliases {
			if rootNames[workflowLookupKey(alias)] {
				return nil, fmt.Errorf("workflow command %q alias %q conflicts with an existing generated root command", command.Use, alias)
			}
		}
		rootNames[workflowLookupKey(command.Use)] = true
		for _, alias := range command.Aliases {
			rootNames[workflowLookupKey(alias)] = true
		}
		inputNames := workflowInputNames(command.Inputs)
		seenSteps := map[string]bool{}
		workflow := runtime.WorkflowSpec{
			Use:        command.Use,
			Aliases:    append([]string(nil), command.Aliases...),
			Short:      command.Short,
			Long:       command.Long,
			Example:    command.Example,
			Hidden:     command.Hidden,
			Deprecated: command.Deprecated,
			Params:     workflowInputs(command.Inputs),
			OutputFrom: command.Output.From,
			Output: runtime.OutputHints{
				ListPath:          command.Output.ListPath,
				DefaultColumns:    append([]string(nil), command.Output.DefaultColumns...),
				ResponseMediaType: command.Output.ResponseMediaType,
			},
		}
		for _, step := range command.Steps {
			ref, err := lookup.resolve(step.Uses)
			if err != nil {
				return nil, fmt.Errorf("workflow command %q step %q: %w", command.Use, step.ID, err)
			}
			if err := validateWorkflowStepParams(step, ref); err != nil {
				return nil, fmt.Errorf("workflow command %q step %q: %w", command.Use, step.ID, err)
			}
			if err := validateWorkflowStepRefs(step, inputNames, seenSteps); err != nil {
				return nil, fmt.Errorf("workflow command %q step %q: %w", command.Use, step.ID, err)
			}
			workflow.Steps = append(workflow.Steps, runtime.WorkflowStepSpec{
				ID:             step.ID,
				Operation:      ref,
				When:           workflowConditions(step.When),
				Params:         maps.Clone(step.Params),
				BodySets:       workflowValues(step.Set),
				BodyStringSets: workflowValues(step.SetStr),
			})
			seenSteps[step.ID] = true
		}
		if err := validateWorkflowRefs(command.Output.From, inputNames, seenSteps); err != nil {
			return nil, fmt.Errorf("workflow command %q output.from: %w", command.Use, err)
		}
		out = append(out, workflow)
	}
	return out, nil
}

type workflowLookup struct {
	refs      map[string]runtime.CommandSpec
	ambiguous map[string]bool
}

func workflowOperationLookup(modules []app.Module) workflowLookup {
	lookup := workflowLookup{
		refs:      map[string]runtime.CommandSpec{},
		ambiguous: map[string]bool{},
	}
	for _, module := range modules {
		for _, spec := range module.Specs {
			if spec.OperationID != "" {
				lookup.add(spec.OperationID, spec)
				lookup.add(module.Source+"."+spec.OperationID, spec)
				lookup.add(module.CLIName+"."+spec.OperationID, spec)
			}
			group := workflowCommandSegment(spec.Group)
			use := workflowCommandSegment(spec.Use)
			if group != "" && use != "" {
				lookup.add(module.Source+" "+group+" "+use, spec)
				lookup.add(module.CLIName+" "+group+" "+use, spec)
				lookup.add(module.Source+"."+group+"."+use, spec)
				lookup.add(module.CLIName+"."+group+"."+use, spec)
				if module.Flat {
					lookup.add(group+" "+use, spec)
					lookup.add(group+"."+use, spec)
				}
			}
		}
	}
	return lookup
}

func (l workflowLookup) add(key string, spec runtime.CommandSpec) {
	key = workflowLookupKey(key)
	if key == "" {
		return
	}
	if existing, exists := l.refs[key]; exists {
		if existing.OperationID == spec.OperationID &&
			existing.Method == spec.Method &&
			existing.PathTpl == spec.PathTpl &&
			existing.Use == spec.Use &&
			existing.Group == spec.Group {
			return
		}
		l.ambiguous[key] = true
		return
	}
	l.refs[key] = spec
}

func (l workflowLookup) resolve(raw string) (runtime.CommandSpec, error) {
	key := workflowLookupKey(raw)
	if l.ambiguous[key] {
		return runtime.CommandSpec{}, fmt.Errorf("operation reference %q is ambiguous", raw)
	}
	ref, ok := l.refs[key]
	if !ok {
		return runtime.CommandSpec{}, fmt.Errorf("operation reference %q was not found", raw)
	}
	return ref, nil
}

func workflowLookupKey(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.Join(strings.Fields(raw), " ")
	return raw
}

func workflowCommandSegment(raw string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func workflowRootNames(modules []app.Module, shortcuts []string) map[string]bool {
	names := map[string]bool{}
	for _, shortcut := range shortcuts {
		names[workflowLookupKey(shortcut)] = true
	}
	for _, module := range modules {
		if !module.Flat {
			names[workflowLookupKey(module.CLIName)] = true
			continue
		}
		for _, spec := range module.Specs {
			name := workflowCommandSegment(spec.Group)
			if name != "" {
				names[workflowLookupKey(name)] = true
			}
		}
	}
	return names
}

func workflowInputNames(inputs []config.WorkflowInput) map[string]bool {
	names := map[string]bool{}
	for _, input := range inputs {
		names[input.Name] = true
		names[input.Flag] = true
	}
	return names
}

func validateWorkflowStepParams(step config.WorkflowStep, spec runtime.CommandSpec) error {
	if len(step.Params) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, param := range spec.Params {
		allowed[param.Name] = true
		allowed[param.Flag] = true
	}
	for key := range step.Params {
		if !allowed[key] {
			return fmt.Errorf("param %q does not match operation %q", key, workflowOperationName(spec))
		}
	}
	return nil
}

func workflowOperationName(spec runtime.CommandSpec) string {
	if spec.OperationID != "" {
		return spec.OperationID
	}
	return strings.TrimSpace(spec.Group + " " + spec.Use)
}

func validateWorkflowStepRefs(step config.WorkflowStep, inputs map[string]bool, steps map[string]bool) error {
	for i, cond := range step.When {
		if err := validateWorkflowRefs(cond.Value, inputs, steps); err != nil {
			return fmt.Errorf("when[%d]: %w", i, err)
		}
	}
	for key, expr := range step.Params {
		if err := validateWorkflowRefs(expr, inputs, steps); err != nil {
			return fmt.Errorf("param %q: %w", key, err)
		}
	}
	for key, expr := range step.Set {
		if err := validateWorkflowRefs(expr, inputs, steps); err != nil {
			return fmt.Errorf("set %q: %w", key, err)
		}
	}
	for key, expr := range step.SetStr {
		if err := validateWorkflowRefs(expr, inputs, steps); err != nil {
			return fmt.Errorf("set_str %q: %w", key, err)
		}
	}
	return nil
}

func validateWorkflowRefs(expr string, inputs map[string]bool, steps map[string]bool) error {
	refs, err := workflowRefs(expr)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		switch {
		case strings.HasPrefix(ref, "input."):
			name := strings.TrimPrefix(ref, "input.")
			if !inputs[name] {
				return fmt.Errorf("unknown input %q", name)
			}
		case strings.HasPrefix(ref, "steps."):
			rest := strings.TrimPrefix(ref, "steps.")
			id, _, _ := strings.Cut(rest, ".")
			if !steps[id] {
				return fmt.Errorf("unknown or forward step %q", id)
			}
		default:
			return fmt.Errorf("unknown reference %q", ref)
		}
	}
	return nil
}

func workflowRefs(expr string) ([]string, error) {
	var refs []string
	rest := expr
	for {
		start := strings.Index(rest, "${")
		if start < 0 {
			return refs, nil
		}
		after := rest[start+2:]
		end := strings.Index(after, "}")
		if end < 0 {
			return nil, fmt.Errorf("unterminated reference in %q", expr)
		}
		refs = append(refs, strings.TrimSpace(after[:end]))
		rest = after[end+1:]
	}
}

func workflowInputs(inputs []config.WorkflowInput) []runtime.ParamSpec {
	out := make([]runtime.ParamSpec, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, runtime.ParamSpec{
			Name:       input.Name,
			Flag:       input.Flag,
			In:         runtime.InInput,
			GoType:     input.Type,
			Help:       input.Help,
			Required:   input.Required,
			Default:    input.Default,
			Enum:       append([]string(nil), input.Enum...),
			Format:     input.Format,
			Deprecated: input.Deprecated,
		})
	}
	return out
}

func workflowConditions(conditions []config.WorkflowCondition) []runtime.WorkflowCondition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]runtime.WorkflowCondition, 0, len(conditions))
	for _, cond := range conditions {
		out = append(out, runtime.WorkflowCondition{
			Value:    cond.Value,
			Operator: cond.Operator,
			Values:   append([]string(nil), cond.Values...),
		})
	}
	return out
}

func workflowValues(values map[string]string) []runtime.WorkflowValue {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]runtime.WorkflowValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, runtime.WorkflowValue{Name: key, Value: values[key]})
	}
	return out
}
