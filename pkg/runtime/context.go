package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

func resolveCommandContexts(cmd *cobra.Command, spec CommandSpec, input *OperationInput) error {
	if !needsStoredContext(spec, *input) {
		return resolveOperationContexts(spec, input, "")
	}
	res, err := resolveHost(cmd, spec.DefaultHostname)
	if err != nil {
		for _, param := range spec.Params {
			if param.Context != "" && param.Required && contextNeedsStored(param, *input) {
				return err
			}
		}
		return nil
	}
	return resolveOperationContexts(spec, input, res.Hostname)
}

func needsStoredContext(spec CommandSpec, input OperationInput) bool {
	for _, param := range spec.Params {
		if param.Context != "" && contextNeedsStored(param, input) {
			return true
		}
	}
	return false
}

func contextNeedsStored(param ParamSpec, input OperationInput) bool {
	if contextValueAvailable(param, input) {
		return false
	}
	info, ok := config.Active().Contexts[param.Context]
	return !ok || info.Env == "" || strings.TrimSpace(os.Getenv(info.Env)) == ""
}

func contextValueAvailable(param ParamSpec, input OperationInput) bool {
	if input.Changed != nil {
		return input.Changed[boundParamKey(param)] || input.Changed[param.Name] || input.Changed[param.Flag]
	}
	for _, key := range []string{boundParamKey(param), param.Name, param.Flag} {
		if _, ok := input.Values[key]; ok {
			return true
		}
	}
	return false
}

func resolveOperationContexts(spec CommandSpec, input *OperationInput, hostname string) error {
	var entry config.HostEntry
	if hostname != "" {
		hosts, err := config.LoadHosts()
		if err != nil {
			return err
		}
		entry, _ = hosts.Get(hostname)
	}
	if input.Values == nil {
		input.Values = map[string]any{}
	}
	for _, param := range spec.Params {
		if param.Context == "" || contextValueAvailable(param, *input) {
			continue
		}
		info, ok := config.Active().Contexts[param.Context]
		if !ok {
			return fmt.Errorf("parameter %q references unknown context %q", param.Name, param.Context)
		}
		if param.GoType != "string" {
			return fmt.Errorf("context parameter %q must be a string", param.Name)
		}
		value := ""
		if info.Env != "" {
			value = strings.TrimSpace(os.Getenv(info.Env))
		}
		if value == "" {
			value = strings.TrimSpace(entry.Contexts[param.Context])
		}
		if value == "" {
			if param.Required {
				return missingContextError(param, info)
			}
			continue
		}
		key := boundParamKey(param)
		input.Values[key] = value
		if input.Changed != nil {
			input.Changed[key] = true
		}
	}
	return nil
}

func missingContextError(param ParamSpec, info config.ContextInfo) error {
	hint := fmt.Sprintf("pass --%s", param.Flag)
	if info.Env != "" {
		hint += fmt.Sprintf(" or set $%s", info.Env)
	}
	hint += fmt.Sprintf("; inspect stored values with `%s auth context status -o json`", config.Active().CLI.Name)
	cause := fmt.Errorf("context %q has no value for --%s", param.Context, param.Flag)
	return NewError(CodeUsage, ExitUsage, "active context required", hint, cause)
}

func persistOperationContext(ctx context.Context, spec CommandSpec, input OperationInput, hostname string) error {
	if spec.SetContext == nil {
		return nil
	}
	if _, ok := config.Active().Contexts[spec.SetContext.Name]; !ok {
		return fmt.Errorf("operation sets unknown context %q", spec.SetContext.Name)
	}
	index, ok := runtimeParamIndex(spec.Params, spec.SetContext.Param)
	if !ok {
		return fmt.Errorf("context source parameter %q not found", spec.SetContext.Param)
	}
	if spec.Params[index].GoType != "string" {
		return fmt.Errorf("context source parameter %q must be a string", spec.SetContext.Param)
	}
	value, present, err := operationValue(input, spec.Params[index])
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("context source parameter %q is empty", spec.SetContext.Param)
	}
	contextValue := strings.TrimSpace(operationStringValue(value))
	if contextValue == "" {
		return fmt.Errorf("context source parameter %q is empty", spec.SetContext.Param)
	}
	if hostname == "" {
		return fmt.Errorf("operation succeeded but context %q could not be saved: hostname is empty", spec.SetContext.Name)
	}
	if err := config.MutateHosts(ctx, func(hosts *config.Hosts) error {
		entry, ok := hosts.Get(hostname)
		if !ok {
			return notAuthenticatedToHost(hostname)
		}
		if entry.Contexts == nil {
			entry.Contexts = map[string]string{}
		}
		entry.Contexts[spec.SetContext.Name] = contextValue
		hosts.Set(hostname, entry)
		return nil
	}); err != nil {
		return fmt.Errorf("operation succeeded but context %q could not be saved: %w", spec.SetContext.Name, err)
	}
	return nil
}
