package render

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func ValidateOverlayModule(specs []runtime.CommandSpec, mod overlay.Module) error {
	legacyUses := legacyOverlayUses(specs)
	for i, spec := range specs {
		override, ok := commandOverride(mod, spec, legacyUses[i])
		if !ok {
			continue
		}
		if err := validateArguments(spec, override.Params); err != nil {
			return fmt.Errorf("command %q: %w", spec.Use, err)
		}
		if override.Output != nil {
			if err := validateDefaultColumnsOverride(override.Output.DefaultColumns); err != nil {
				return fmt.Errorf("command %q output: %w", spec.Use, err)
			}
			columns := spec.Output.DefaultColumns
			if len(override.Output.DefaultColumns) > 0 {
				columns = override.Output.DefaultColumns
			}
			if err := validateColumnLabelsOverride(columns, override.Output.ColumnLabels); err != nil {
				return fmt.Errorf("command %q output: %w", spec.Use, err)
			}
			if err := validateColumnFormatsOverride(columns, override.Output.ColumnFormats); err != nil {
				return fmt.Errorf("command %q output: %w", spec.Use, err)
			}
			if err := validateColumnAlignmentsOverride(columns, override.Output.ColumnAlignments); err != nil {
				return fmt.Errorf("command %q output: %w", spec.Use, err)
			}
		}
		if override.Output != nil && override.Output.Streaming != nil {
			if err := validateStreamingOverride(spec, *override.Output.Streaming); err != nil {
				return fmt.Errorf("command %q stream policy: %w", spec.Use, err)
			}
		}
		if override.Body != nil && override.Body.Flags {
			if _, _, err := normalize.ExpandJSONBodyFlags(spec); err != nil {
				return fmt.Errorf("command %q body.flags: %w", spec.Use, err)
			}
		}
	}
	merged, err := mergeOverlaySpecs(specs, mod)
	if err != nil {
		return err
	}
	if err := validateJSONBodyFlagParams(merged); err != nil {
		return err
	}
	if err := validateGroupOverrides(merged, mod.Groups); err != nil {
		return err
	}
	return validateRuntimeSchemaBindings(specs, merged, mod)
}

func validateJSONBodyFlagParams(specs []runtime.CommandSpec) error {
	for _, spec := range specs {
		if err := normalize.ValidateJSONBodyFlagParams(spec); err != nil {
			return fmt.Errorf("command %q body.flags: %w", spec.Use, err)
		}
	}
	return nil
}

func validateGroupOverrides(specs []runtime.CommandSpec, groups map[string]overlay.GroupOverride) error {
	known := make(map[string]bool, len(specs))
	for _, spec := range specs {
		known[spec.Group] = true
	}
	for name, group := range groups {
		if !known[name] {
			return fmt.Errorf("group %q does not exist after command overrides", name)
		}
		if group.Short == "" || strings.TrimSpace(group.Short) != group.Short || strings.ContainsAny(group.Short, "\r\n") {
			return fmt.Errorf("group %q short must be one non-empty trimmed line", name)
		}
	}
	return nil
}

func validateArguments(spec runtime.CommandSpec, overrides map[string]overlay.ParamOverride) error {
	seenNames := map[string]bool{}
	for paramName, override := range overrides {
		if override.Argument == "" {
			continue
		}
		matches := 0
		for _, param := range spec.Params {
			if param.Name == paramName {
				matches++
			}
		}
		if matches == 0 {
			return fmt.Errorf("argument parameter %q does not exist", paramName)
		}
		if matches > 1 {
			return fmt.Errorf("argument parameter %q is ambiguous", paramName)
		}
		if !validArgumentName(override.Argument) {
			return fmt.Errorf("argument name %q must contain only letters, digits, dots, underscores, or hyphens", override.Argument)
		}
		if seenNames[override.Argument] {
			return fmt.Errorf("argument name %q is mapped more than once", override.Argument)
		}
		seenNames[override.Argument] = true
	}
	return nil
}

func validArgumentName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validateDefaultColumnsOverride(columns []string) error {
	seen := map[string]bool{}
	for i, column := range columns {
		if column == "" || strings.TrimSpace(column) != column {
			return fmt.Errorf("default_columns[%d] must be a non-empty trimmed path", i)
		}
		for _, part := range strings.Split(column, ".") {
			if strings.TrimSpace(part) == "" {
				return fmt.Errorf("default_columns[%d] contains an empty path segment", i)
			}
		}
		if seen[column] {
			return fmt.Errorf("default column %q is duplicated", column)
		}
		seen[column] = true
	}
	return nil
}

func validateColumnLabelsOverride(columns []string, labels map[string]string) error {
	known := make(map[string]bool, len(columns))
	for _, column := range columns {
		known[column] = true
	}
	for _, path := range slices.Sorted(maps.Keys(labels)) {
		label := labels[path]
		if !known[path] {
			return fmt.Errorf("column label %q does not match a default column", path)
		}
		if label == "" || strings.TrimSpace(label) != label || strings.ContainsAny(label, "\t\v\f\r\n") {
			return fmt.Errorf("column label %q must be non-empty, trimmed, and single-line", path)
		}
	}
	return nil
}

func validateColumnFormatsOverride(columns []string, formats map[string]overlay.ColumnFormatOverride) error {
	known := make(map[string]bool, len(columns))
	for _, column := range columns {
		known[column] = true
	}
	for _, path := range slices.Sorted(maps.Keys(formats)) {
		format := formats[path]
		if !known[path] {
			return fmt.Errorf("column format %q does not match a default column", path)
		}
		if format.Kind != "currency" {
			return fmt.Errorf("column format %q kind must be currency", path)
		}
		if !validCurrencyCode(format.Currency) {
			return fmt.Errorf("column format %q currency must be a three-letter uppercase code", path)
		}
		if format.SourceScale < 0 || format.SourceScale > 18 {
			return fmt.Errorf("column format %q source_scale must be between 0 and 18", path)
		}
		if format.MinFractionDigits < 0 || format.MinFractionDigits > 18 {
			return fmt.Errorf("column format %q min_fraction_digits must be between 0 and 18", path)
		}
		if format.MaxFractionDigits < format.MinFractionDigits || format.MaxFractionDigits > 18 {
			return fmt.Errorf("column format %q max_fraction_digits must be between min_fraction_digits and 18", path)
		}
		if format.MaxFractionDigits < format.SourceScale {
			return fmt.Errorf("column format %q max_fraction_digits must be at least source_scale", path)
		}
	}
	return nil
}

func validateColumnAlignmentsOverride(columns []string, alignments map[string]string) error {
	known := make(map[string]bool, len(columns))
	for _, column := range columns {
		known[column] = true
	}
	for _, path := range slices.Sorted(maps.Keys(alignments)) {
		align := alignments[path]
		if !known[path] {
			return fmt.Errorf("column alignment %q does not match a default column", path)
		}
		if align != "left" && align != "right" {
			return fmt.Errorf("column alignment %q must be left or right", path)
		}
	}
	return nil
}

func validCurrencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func validateStreamingOverride(spec runtime.CommandSpec, stream overlay.StreamingOverride) error {
	if spec.Output.Streaming == nil {
		return fmt.Errorf("operation is not declared as streaming")
	}
	if spec.Output.Streaming.Strategy != "sse" && spec.Output.Streaming.Strategy != "ndjson" {
		return fmt.Errorf("unsupported strategy %q", spec.Output.Streaming.Strategy)
	}
	if stream.Data != "json" {
		return fmt.Errorf("data must be json")
	}
	if stream.Collect == nil {
		return fmt.Errorf("collect is required")
	}
	if spec.Output.Streaming.Strategy == "ndjson" && stream.EventNamePath == "" {
		return fmt.Errorf("event_name_path is required for ndjson")
	}
	if stream.Collect.RequireStop && len(stream.Collect.StopEvents)+len(stream.Collect.PauseEvents)+len(stream.Collect.ErrorEvents) == 0 {
		return fmt.Errorf("require_stop needs a terminal event")
	}
	terminal := map[string]string{}
	for kind, events := range map[string][]string{
		"stop": stream.Collect.StopEvents, "pause": stream.Collect.PauseEvents, "error": stream.Collect.ErrorEvents,
	} {
		for _, event := range events {
			if event == "" {
				return fmt.Errorf("%s event must not be empty", kind)
			}
			if previous := terminal[event]; previous != "" {
				return fmt.Errorf("event %q is both %s and %s", event, previous, kind)
			}
			terminal[event] = kind
		}
	}
	for i, field := range stream.Collect.Fields {
		if len(field.Events) == 0 || field.To == "" {
			return fmt.Errorf("field %d needs events and to", i)
		}
		if (field.From == "") == (field.Value == "") {
			return fmt.Errorf("field %d needs exactly one of from or value", i)
		}
		switch field.Reduce {
		case "first", "last", "concat", "append":
		default:
			return fmt.Errorf("field %d has unsupported reducer %q", i, field.Reduce)
		}
	}
	if stream.Live != nil && (len(stream.Live.Events) == 0 || stream.Live.From == "") {
		return fmt.Errorf("live needs events and from")
	}
	return nil
}

func validateMutationOverride(override overlay.Override) error {
	switch override.Mutation {
	case "", "read", "write":
		return nil
	default:
		return fmt.Errorf("mutation must be %q or %q, got %q", "read", "write", override.Mutation)
	}
}
