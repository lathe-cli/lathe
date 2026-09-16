package runtime

import (
	"maps"

	"github.com/lathe-cli/lathe/pkg/config"
)

func catalogCommand(service string, spec CommandSpec, path []string) CatalogCommand {
	flags := catalogFlags(spec.Params)
	examples := spec.Examples
	if len(examples) == 0 && spec.Example != "" {
		examples = []CommandExample{{Command: spec.Example}}
	}
	cmd := CatalogCommand{
		Kind:          "operation",
		Path:          append([]string(nil), path...),
		Service:       service,
		Group:         spec.Group,
		Use:           spec.Use,
		Aliases:       append([]string(nil), spec.Aliases...),
		Shortcuts:     cloneShortcuts(spec.Shortcuts),
		Summary:       spec.Short,
		Description:   spec.Long,
		Example:       spec.Example,
		Examples:      examples,
		OperationID:   spec.OperationID,
		HTTP:          CatalogHTTP{Method: spec.Method, PathTemplate: spec.PathTpl, DefaultHostname: spec.DefaultHostname},
		Auth:          catalogAuth(spec.Security),
		Mutation:      catalogMutation(spec),
		DryRun:        &CatalogDryRun{Mode: DryRunUnsupported},
		Flags:         flags,
		Output:        catalogOutput(spec.Output),
		Hidden:        spec.Hidden,
		Deprecated:    spec.Deprecated,
		Notes:         append([]string(nil), spec.Notes...),
		Prerequisites: append([]string(nil), spec.Prerequisites...),
		KnownErrors:   append([]KnownError(nil), spec.KnownErrors...),
		SearchTerms:   append([]string(nil), spec.SearchTerms...),
	}
	if spec.SetContext != nil {
		cmd.SetsContext = &CatalogContextSet{Name: spec.SetContext.Name, FromParam: spec.SetContext.Param}
	}
	if spec.RequestBody != nil {
		cmd.Body = &CatalogBody{
			Required:      spec.RequestBody.Required,
			MediaType:     spec.RequestBody.MediaType,
			Schema:        spec.RequestBody.Schema,
			Template:      spec.RequestBody.Template,
			MergePath:     spec.RequestBody.MergePath,
			SetOnlyFields: append([]string(nil), spec.RequestBody.SetOnlyFields...),
		}
		if binding := spec.RequestBody.RuntimeSchema; binding != nil {
			cmd.Body.RuntimeSchema = &CatalogRuntimeSchema{
				OperationID: binding.Operation.OperationID,
				HTTP: CatalogHTTP{
					Method:          binding.Operation.Method,
					PathTemplate:    binding.Operation.PathTpl,
					DefaultHostname: binding.Operation.DefaultHostname,
				},
				ResponsePath: binding.ResponsePath,
				Params:       cloneCatalogMap(binding.Params),
				Contexts:     catalogContextBindings(binding.Operation.Params),
			}
		}
	}
	return cmd
}

func catalogWorkflowConditions(conditions []WorkflowCondition) []CatalogWorkflowCondition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]CatalogWorkflowCondition, 0, len(conditions))
	for _, cond := range conditions {
		out = append(out, CatalogWorkflowCondition{
			Value:    cond.Value,
			Operator: cond.Operator,
			Values:   append([]string(nil), cond.Values...),
		})
	}
	return out
}

func catalogWorkflowCommand(spec WorkflowSpec, path []string) CatalogCommand {
	flags := catalogFlags(spec.Params)
	steps := make([]CatalogWorkflowStep, 0, len(spec.Steps))
	auth := CatalogAuth{Required: false}
	for _, step := range spec.Steps {
		catalogStep := CatalogWorkflowStep{
			ID:          step.ID,
			OperationID: step.Operation.OperationID,
			HTTP: CatalogHTTP{
				Method:          step.Operation.Method,
				PathTemplate:    step.Operation.PathTpl,
				DefaultHostname: step.Operation.DefaultHostname,
			},
			When:     catalogWorkflowConditions(step.When),
			Contexts: catalogContextBindings(step.Operation.Params),
		}
		if step.Operation.SetContext != nil {
			catalogStep.SetsContext = &CatalogContextSet{Name: step.Operation.SetContext.Name, FromParam: step.Operation.SetContext.Param}
		}
		steps = append(steps, catalogStep)
		stepAuth := catalogAuth(step.Operation.Security)
		if stepAuth.Required {
			auth.Required = true
		}
		auth.Scopes = append(auth.Scopes, stepAuth.Scopes...)
	}
	auth.Scopes = normalizeCapabilities(auth.Scopes)
	return CatalogCommand{
		Kind:        "workflow",
		Path:        append([]string(nil), path...),
		Service:     "workflow",
		Group:       "workflow",
		Use:         spec.Use,
		Aliases:     append([]string(nil), spec.Aliases...),
		Summary:     spec.Short,
		Description: spec.Long,
		Example:     spec.Example,
		HTTP:        CatalogHTTP{},
		Workflow: &CatalogWorkflow{
			DSL:        "lathe.workflow.v1",
			OutputFrom: spec.OutputFrom,
			Steps:      steps,
		},
		Auth:       auth,
		Mutation:   catalogWorkflowMutation(spec),
		DryRun:     &CatalogDryRun{Mode: DryRunUnsupported},
		Flags:      flags,
		Output:     catalogOutput(spec.Output),
		Hidden:     spec.Hidden,
		Deprecated: spec.Deprecated,
	}
}

func catalogFlags(params []ParamSpec) []CatalogFlag {
	flags := make([]CatalogFlag, 0, len(params))
	position := 0
	for _, p := range params {
		var inputModes []string
		if isSensitiveStringParam(p) {
			inputModes = []string{"flag", "env", "file", "stdin"}
		}
		argumentPosition := 0
		if p.Argument != "" {
			position++
			argumentPosition = position
		}
		flags = append(flags, CatalogFlag{
			Name:       p.Name,
			Flag:       p.Flag,
			Aliases:    append([]string(nil), p.Aliases...),
			Argument:   p.Argument,
			Position:   argumentPosition,
			Location:   p.In,
			Type:       p.GoType,
			Required:   p.Required,
			Default:    p.Default,
			Enum:       append([]string(nil), p.Enum...),
			ItemEnum:   append([]string(nil), p.ItemEnum...),
			Format:     p.Format,
			InputModes: inputModes,
			Deprecated: p.Deprecated,
			Help:       p.Help,
			Context:    catalogContextBinding(p),
		})
	}
	return flags
}

func catalogContextBinding(param ParamSpec) *CatalogContextBinding {
	if param.Context == "" {
		return nil
	}
	info := config.Active().Contexts[param.Context]
	precedence := []string{"flag"}
	if info.Env != "" {
		precedence = append(precedence, "env")
	}
	precedence = append(precedence, "stored")
	return &CatalogContextBinding{Name: param.Context, Env: info.Env, Precedence: precedence}
}

func catalogContextBindings(params []ParamSpec) []CatalogContextBinding {
	out := make([]CatalogContextBinding, 0)
	for _, param := range params {
		if binding := catalogContextBinding(param); binding != nil {
			out = append(out, *binding)
		}
	}
	return out
}

func cloneShortcuts(shortcuts []CommandShortcut) []CommandShortcut {
	out := make([]CommandShortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		out = append(out, CommandShortcut{Use: shortcut.Use, Params: cloneCatalogMap(shortcut.Params)})
	}
	return out
}

func cloneCatalogMap[T any](in map[string]T) map[string]T {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}

func catalogPagination(p *PaginationHint) *CatalogPagination {
	if p == nil {
		return nil
	}
	return &CatalogPagination{
		Strategy:   p.Strategy,
		TokenParam: p.TokenParam,
		TokenField: p.TokenField,
		LimitParam: p.LimitParam,
	}
}

func catalogStreaming(s *StreamingHint) *CatalogStreaming {
	if s == nil {
		return nil
	}
	return &CatalogStreaming{Strategy: s.Strategy, Policy: s.Policy}
}

func catalogAuth(security *SecurityHint) CatalogAuth {
	if security == nil {
		return CatalogAuth{Required: true}
	}
	return CatalogAuth{Required: !security.Public, Scopes: append([]string(nil), security.Scopes...)}
}

func catalogOutput(output OutputHints) CatalogOutput {
	return CatalogOutput{
		ListPath:          output.ListPath,
		DefaultColumns:    append([]string(nil), output.DefaultColumns...),
		ColumnLabels:      cloneCatalogMap(output.ColumnLabels),
		ColumnFormats:     cloneCatalogMap(output.ColumnFormats),
		ColumnAlignments:  cloneCatalogMap(output.ColumnAlignments),
		ResponseMediaType: output.ResponseMediaType,
		Pagination:        catalogPagination(output.Pagination),
		Streaming:         catalogStreaming(output.Streaming),
	}
}
