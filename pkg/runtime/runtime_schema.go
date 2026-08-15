package runtime

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const runtimeSchemaResource = "urn:lathe:runtime-schema"

func validateRuntimeSchemaBody(ctx context.Context, target CommandSpec, input OperationInput, body any, opts OperationOptions) error {
	binding := target.RequestBody.RuntimeSchema
	if binding == nil || body == nil {
		return nil
	}
	instance, err := requestBodyJSONValue(body)
	if err != nil {
		return runtimeSchemaUsageError(fmt.Errorf("decode request body: %w", err))
	}

	source, sourceInput, err := runtimeSchemaInput(target, input, *binding)
	if err != nil {
		return runtimeSchemaUsageError(err)
	}
	source.SetContext = nil
	sourceHostname := ""
	if needsStoredContext(source, sourceInput) {
		sourceHostname = opts.Hostname
	}
	if err := resolveOperationContexts(source, &sourceInput, sourceHostname); err != nil {
		return err
	}
	if err := validateOperationInput(source, sourceInput); err != nil {
		return runtimeSchemaUsageError(fmt.Errorf("runtime schema source: %w", err))
	}
	sourceOpts := opts
	sourceOpts.DryRun = false
	sourceOpts.PaginateAll = false
	sourceOpts.MaxPages = 0
	sourceOpts.Wait = false
	result, err := invokeOperation(ctx, source, sourceInput, sourceOpts, operationOutput{})
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(result.Data))
	if err != nil {
		return newAPIError(fmt.Errorf("decode runtime schema response: %w", err), 0)
	}
	schemaDoc, ok := getNestedPath(doc, binding.ResponsePath)
	if !ok {
		return newAPIError(fmt.Errorf("runtime schema response path %q not found", binding.ResponsePath), 0)
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(nil)
	if err := compiler.AddResource(runtimeSchemaResource, schemaDoc); err != nil {
		return newAPIError(fmt.Errorf("load runtime schema: %w", err), 0)
	}
	schema, err := compiler.Compile(runtimeSchemaResource)
	if err != nil {
		return newAPIError(fmt.Errorf("compile runtime schema: %w", err), 0)
	}
	if err := schema.Validate(instance); err != nil {
		return runtimeSchemaUsageError(err)
	}
	return nil
}

func requestBodyJSONValue(body any) (any, error) {
	raw, _, err := encodeRequestBody(body)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(raw))
}

func runtimeSchemaInput(target CommandSpec, input OperationInput, binding RuntimeSchemaSpec) (CommandSpec, OperationInput, error) {
	source := binding.Operation
	source.Params = append([]ParamSpec(nil), binding.Operation.Params...)
	values := make(map[string]any, len(binding.Params))
	changed := make(map[string]bool, len(binding.Params))
	for key, value := range binding.Params {
		mappedValue := any(value)
		sourceIndex, ok := runtimeParamIndex(source.Params, key)
		if !ok {
			return CommandSpec{}, OperationInput{}, fmt.Errorf("runtime schema source param %q not found", key)
		}
		if targetName, ref := runtimeSchemaReference(value); ref {
			targetIndex, ok := runtimeParamIndex(target.Params, targetName)
			if !ok {
				return CommandSpec{}, OperationInput{}, fmt.Errorf("runtime schema target param %q not found", targetName)
			}
			resolved, present, err := operationValue(input, target.Params[targetIndex])
			if err != nil {
				return CommandSpec{}, OperationInput{}, err
			}
			if !present {
				continue
			}
			mappedValue = resolved
			if isSensitiveStringParam(target.Params[targetIndex]) {
				source.Params[sourceIndex].Format = "password"
			}
		}
		key := boundParamKey(source.Params[sourceIndex])
		values[key] = mappedValue
		changed[key] = true
	}
	return source, OperationInput{Values: values, Changed: changed}, nil
}

func runtimeParamIndex(params []ParamSpec, nameOrFlag string) (int, bool) {
	index := -1
	for i, param := range params {
		if param.Name != nameOrFlag && param.Flag != nameOrFlag {
			continue
		}
		if index != -1 {
			return -1, false
		}
		index = i
	}
	return index, index != -1
}

func runtimeSchemaReference(value string) (string, bool) {
	const prefix = "${params."
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "}") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(value, prefix), "}"), true
}

func runtimeSchemaUsageError(cause error) error {
	return NewError(
		CodeUsage,
		ExitUsage,
		"request body does not match the runtime schema",
		"inspect the command schema and correct the request body",
		cause,
	)
}
