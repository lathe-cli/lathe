package render

import (
	"fmt"
	"strings"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func validateRuntimeSchemaBindings(original, merged []runtime.CommandSpec, mod overlay.Module) error {
	legacyUses := legacyOverlayUses(original)
	for i, spec := range original {
		override, ok := commandOverride(mod, spec, legacyUses[i])
		if !ok || override.Ignore || override.Body == nil || override.Body.RuntimeSchema == nil {
			continue
		}
		target, count := operationByID(merged, spec.OperationID)
		if spec.OperationID == "" || count != 1 {
			return fmt.Errorf("command %q runtime schema target operation_id is missing or ambiguous", spec.Use)
		}
		binding := override.Body.RuntimeSchema
		source, count := operationByID(merged, binding.OperationID)
		if binding.OperationID == "" || count != 1 {
			return fmt.Errorf("command %q runtime schema operation_id %q is missing or ambiguous", spec.Use, binding.OperationID)
		}
		if err := validateRuntimeSchemaSource(*target, *source, binding); err != nil {
			return fmt.Errorf("command %q runtime schema: %w", spec.Use, err)
		}
	}
	return nil
}

func validateRuntimeSchemaSource(target, source runtime.CommandSpec, binding *overlay.RuntimeSchemaOverride) error {
	if target.RequestBody == nil || !jsonMediaType(target.RequestBody.MediaType) {
		return fmt.Errorf("target must have a JSON request body")
	}
	if source.Hidden {
		return fmt.Errorf("source operation %q must be visible", binding.OperationID)
	}
	if !strings.EqualFold(source.Method, "GET") {
		return fmt.Errorf("source operation %q must use GET", binding.OperationID)
	}
	if source.RequestBody != nil {
		return fmt.Errorf("source operation %q must not have a request body", binding.OperationID)
	}
	for _, param := range source.Params {
		if param.In == runtime.InFormData {
			return fmt.Errorf("source operation %q must not have form body parameters", binding.OperationID)
		}
	}
	if source.Output.Streaming != nil {
		return fmt.Errorf("source operation %q must not stream", binding.OperationID)
	}
	if !jsonMediaType(source.Output.ResponseMediaType) {
		return fmt.Errorf("source operation %q must return JSON", binding.OperationID)
	}
	if source.DefaultHostname != target.DefaultHostname {
		return fmt.Errorf("source operation %q must use the target hostname", binding.OperationID)
	}
	if !runtimeSchemaAuthAllowed(target.Security, source.Security) {
		return fmt.Errorf("source operation %q requires stronger auth than the target", binding.OperationID)
	}
	mapped := map[int]bool{}
	for key, value := range binding.Params {
		sourceIndex, count := paramByNameOrFlag(source.Params, key)
		if count != 1 {
			return fmt.Errorf("source param %q is missing or ambiguous", key)
		}
		if mapped[sourceIndex] {
			return fmt.Errorf("source param %q is mapped more than once", source.Params[sourceIndex].Name)
		}
		mapped[sourceIndex] = true
		name, isRef, malformed := runtimeSchemaParamReference(value)
		if malformed {
			return fmt.Errorf("param %q has invalid reference %q", key, value)
		}
		if isRef {
			targetIndex, count := paramByNameOrFlag(target.Params, name)
			if count != 1 {
				return fmt.Errorf("target param %q is missing or ambiguous", name)
			}
			sourceParam := source.Params[sourceIndex]
			targetParam := target.Params[targetIndex]
			if sourceParam.Required && sourceParam.Default == "" && !targetParam.Required && targetParam.Default == "" {
				return fmt.Errorf("required source param %q maps optional target param %q", sourceParam.Name, targetParam.Name)
			}
		}
	}
	for i, param := range source.Params {
		if param.Required && param.Default == "" && param.Context == "" && !mapped[i] {
			return fmt.Errorf("required source param %q is not mapped", param.Name)
		}
	}
	return nil
}

func applyRuntimeSchemaBindings(original, merged []runtime.CommandSpec, mod overlay.Module) {
	legacyUses := legacyOverlayUses(original)
	for i, spec := range original {
		override, ok := commandOverride(mod, spec, legacyUses[i])
		if !ok || override.Ignore || override.Body == nil || override.Body.RuntimeSchema == nil {
			continue
		}
		target, targetCount := operationByID(merged, spec.OperationID)
		source, sourceCount := operationByID(merged, override.Body.RuntimeSchema.OperationID)
		if targetCount != 1 || sourceCount != 1 || target.RequestBody == nil {
			continue
		}
		target.RequestBody.RuntimeSchema = &runtime.RuntimeSchemaSpec{
			Operation:    cloneCommandSpec(*source),
			ResponsePath: override.Body.RuntimeSchema.ResponsePath,
			Params:       copyStringMap(override.Body.RuntimeSchema.Params),
		}
	}
}

func operationByID(specs []runtime.CommandSpec, operationID string) (*runtime.CommandSpec, int) {
	var found *runtime.CommandSpec
	count := 0
	for i := range specs {
		if specs[i].OperationID == operationID {
			found = &specs[i]
			count++
		}
	}
	return found, count
}

func paramByNameOrFlag(params []runtime.ParamSpec, value string) (int, int) {
	index, count := -1, 0
	for i, param := range params {
		if param.Name == value || param.Flag == value {
			index = i
			count++
		}
	}
	return index, count
}

func runtimeSchemaParamReference(value string) (string, bool, bool) {
	const prefix = "${params."
	if !strings.HasPrefix(value, "${") {
		return "", false, false
	}
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "}") {
		return "", false, true
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, prefix), "}")
	return name, true, name == "" || strings.Contains(name, "${") || strings.Contains(name, "}")
}

func jsonMediaType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	return value == "" || value == "application/json" || strings.HasSuffix(value, "+json")
}

func runtimeSchemaAuthAllowed(target, source *runtime.SecurityHint) bool {
	targetRequired := target == nil || !target.Public
	sourceRequired := source == nil || !source.Public
	if !targetRequired && sourceRequired {
		return false
	}
	if !sourceRequired || source == nil || len(source.Scopes) == 0 {
		return true
	}
	targetScopes := map[string]bool{}
	if target != nil {
		for _, scope := range target.Scopes {
			targetScopes[scope] = true
		}
	}
	for _, scope := range source.Scopes {
		if !targetScopes[scope] {
			return false
		}
	}
	return true
}
