package render

import (
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"strconv"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func MergeOverlay(specs []runtime.CommandSpec, overrides map[string]overlay.Override) ([]runtime.CommandSpec, error) {
	return MergeOverlayModule(specs, overlay.Module{Commands: overrides})
}

func MergeOverlayModule(specs []runtime.CommandSpec, mod overlay.Module) ([]runtime.CommandSpec, error) {
	merged, err := mergeOverlaySpecs(specs, mod)
	if err != nil {
		return nil, err
	}
	if err := validateJSONBodyFlagParams(merged); err != nil {
		return nil, err
	}
	applyRuntimeSchemaBindings(specs, merged, mod)
	applyGroupOverrides(merged, mod.Groups)
	return merged, nil
}

func mergeOverlaySpecs(specs []runtime.CommandSpec, mod overlay.Module) ([]runtime.CommandSpec, error) {
	var merged []runtime.CommandSpec
	var overrides []overlay.Override
	var matched []bool
	var matchedLegacyUses []string
	legacyUses := legacyOverlayUses(specs)
	for i, s := range specs {
		o, ok := commandOverride(mod, s, legacyUses[i])
		if ok && o.Ignore {
			continue
		}
		cs := cloneCommandSpec(s)
		merged = append(merged, cs)
		overrides = append(overrides, o)
		matched = append(matched, ok)
		matchedLegacyUses = append(matchedLegacyUses, legacyUses[i])
	}
	disambiguateUse(merged)
	for i := range merged {
		applyBulkDefaults(&merged[i], mod.Defaults, matchedLegacyUses[i])
		if matched[i] {
			if err := validateMutationOverride(overrides[i]); err != nil {
				return nil, fmt.Errorf("command %q: %w", merged[i].Use, err)
			}
			if err := applyBodyFlags(&merged[i], overrides[i]); err != nil {
				return nil, fmt.Errorf("command %q body.flags: %w", merged[i].Use, err)
			}
			applyCommandOverride(&merged[i], overrides[i])
		}
	}
	return merged, nil
}

func legacyOverlayUses(specs []runtime.CommandSpec) []string {
	legacy := append([]runtime.CommandSpec(nil), specs...)
	disambiguateUse(legacy)
	uses := make([]string, len(legacy))
	for i := range legacy {
		uses[i] = legacy[i].Use
	}
	return uses
}

func commandOverride(mod overlay.Module, spec runtime.CommandSpec, legacyUse string) (overlay.Override, bool) {
	if legacyUse != spec.Use {
		if override, ok := mod.Commands[legacyUse]; ok && overrideMatches(spec, override) {
			return override, true
		}
		override, ok := mod.Commands[spec.Use]
		if !ok || override.Match.Method == "" && override.Match.Path == "" {
			return overlay.Override{}, false
		}
		return override, overrideMatches(spec, override)
	}
	override, ok := mod.Commands[spec.Use]
	return override, ok && overrideMatches(spec, override)
}

func disambiguateUse(specs []runtime.CommandSpec) {
	used := map[string]bool{}
	for i := range specs {
		key := specs[i].Group + "\x00" + specs[i].Use
		if !used[key] {
			used[key] = true
			continue
		}
		base := specs[i].Use
		for n := 2; ; n++ {
			candidate := base + "-" + strconv.Itoa(n)
			key = specs[i].Group + "\x00" + candidate
			if !used[key] {
				specs[i].Use = candidate
				used[key] = true
				break
			}
		}
	}
}

func applyGroupOverrides(specs []runtime.CommandSpec, groups map[string]overlay.GroupOverride) {
	for i := range specs {
		if group, ok := groups[specs[i].Group]; ok {
			specs[i].GroupShort = group.Short
		}
	}
}

func overrideMatches(spec runtime.CommandSpec, override overlay.Override) bool {
	if override.Match.Method != "" && !strings.EqualFold(override.Match.Method, spec.Method) {
		return false
	}
	if override.Match.Path != "" && override.Match.Path != spec.PathTpl {
		return false
	}
	return true
}

func cloneCommandSpec(spec runtime.CommandSpec) runtime.CommandSpec {
	cloned := spec
	cloned.Aliases = append([]string(nil), spec.Aliases...)
	cloned.Examples = append([]runtime.CommandExample(nil), spec.Examples...)
	for i := range cloned.Examples {
		cloned.Examples[i].FollowUpCommands = append([]string(nil), spec.Examples[i].FollowUpCommands...)
	}
	cloned.Shortcuts = append([]runtime.CommandShortcut(nil), spec.Shortcuts...)
	for i := range cloned.Shortcuts {
		cloned.Shortcuts[i].Params = copyStringMap(spec.Shortcuts[i].Params)
	}
	cloned.Notes = append([]string(nil), spec.Notes...)
	cloned.Prerequisites = append([]string(nil), spec.Prerequisites...)
	cloned.KnownErrors = append([]runtime.KnownError(nil), spec.KnownErrors...)
	cloned.SearchTerms = append([]string(nil), spec.SearchTerms...)
	if spec.SetContext != nil {
		setContext := *spec.SetContext
		cloned.SetContext = &setContext
	}
	cloned.Params = append([]runtime.ParamSpec(nil), spec.Params...)
	for i := range cloned.Params {
		cloned.Params[i].Aliases = append([]string(nil), spec.Params[i].Aliases...)
		cloned.Params[i].Enum = append([]string(nil), spec.Params[i].Enum...)
		cloned.Params[i].ItemEnum = append([]string(nil), spec.Params[i].ItemEnum...)
	}
	if spec.RequestBody != nil {
		body := *spec.RequestBody
		if spec.RequestBody.RuntimeSchema != nil {
			runtimeSchema := *spec.RequestBody.RuntimeSchema
			runtimeSchema.Params = copyStringMap(spec.RequestBody.RuntimeSchema.Params)
			body.RuntimeSchema = &runtimeSchema
		}
		cloned.RequestBody = &body
	}
	if spec.Output.Streaming != nil {
		streaming := *spec.Output.Streaming
		cloned.Output.Streaming = &streaming
	}
	return cloned
}

func applyBulkDefaults(spec *runtime.CommandSpec, defaults overlay.Defaults, legacyUse string) {
	if defaults.Pagination == nil || !matchesAny(defaults.Pagination.MatchCommands, spec.Use) && !matchesAny(defaults.Pagination.MatchCommands, legacyUse) {
		return
	}
	for i := range spec.Params {
		if spec.Params[i].Default != "" {
			continue
		}
		if value, ok := defaults.Pagination.Params[spec.Params[i].Name]; ok {
			spec.Params[i].Default = value
		}
	}
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		matched, err := path.Match(pattern, value)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func applyBodyFlags(spec *runtime.CommandSpec, override overlay.Override) error {
	if override.Body == nil || !override.Body.Flags {
		return nil
	}
	params, setOnly, err := normalize.ExpandJSONBodyFlags(*spec)
	if err != nil {
		return err
	}
	spec.Params = append(spec.Params, params...)
	spec.RequestBody.SetOnlyFields = setOnly
	return nil
}

func setString(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}

func applyCommandOverride(spec *runtime.CommandSpec, override overlay.Override) {
	setString(&spec.Use, override.Use)
	setString(&spec.Short, override.Short)
	setString(&spec.Long, override.Long)
	setString(&spec.Example, override.Example)
	if len(override.Examples) > 0 {
		spec.Examples = make([]runtime.CommandExample, 0, len(override.Examples))
		for _, example := range override.Examples {
			spec.Examples = append(spec.Examples, runtimeCommandExample(example))
		}
	}
	if len(override.Notes) > 0 {
		spec.Notes = append([]string(nil), override.Notes...)
	}
	if len(override.Prerequisites) > 0 {
		spec.Prerequisites = append([]string(nil), override.Prerequisites...)
	}
	if len(override.KnownErrors) > 0 {
		spec.KnownErrors = make([]runtime.KnownError, 0, len(override.KnownErrors))
		for _, ke := range override.KnownErrors {
			spec.KnownErrors = append(spec.KnownErrors, runtime.KnownError(ke))
		}
	}
	setString(&spec.Mutation, override.Mutation)
	if len(override.SearchTerms) > 0 {
		spec.SearchTerms = append([]string(nil), override.SearchTerms...)
	}
	if len(override.Aliases) > 0 {
		spec.Aliases = append(spec.Aliases, override.Aliases...)
	}
	for _, shortcut := range override.Shortcuts {
		spec.Shortcuts = append(spec.Shortcuts, runtime.CommandShortcut{
			Use:    shortcut.Use,
			Params: copyStringMap(shortcut.Params),
		})
	}
	setString(&spec.Group, override.Group)
	if override.Hidden != nil {
		spec.Hidden = *override.Hidden
	}
	if len(override.Params) > 0 {
		for j := range spec.Params {
			po, pok := override.Params[spec.Params[j].Name]
			if !pok {
				continue
			}
			setString(&spec.Params[j].Flag, po.Flag)
			setString(&spec.Params[j].Argument, po.Argument)
			setString(&spec.Params[j].Help, po.Help)
			spec.Params[j].Required = spec.Params[j].Required || po.Required
			setString(&spec.Params[j].Default, po.Default)
			spec.Params[j].Deprecated = spec.Params[j].Deprecated || po.Deprecated || po.DeprecatedAlias
			setString(&spec.Params[j].Context, po.Context)
		}
	}
	if override.Context != nil && override.Context.SetOnSuccess != nil {
		set := override.Context.SetOnSuccess
		spec.SetContext = &runtime.ContextSetHint{Name: set.Name, Param: set.FromParam}
	}
	if override.Output != nil && len(override.Output.DefaultColumns) > 0 {
		spec.Output.DefaultColumns = append([]string(nil), override.Output.DefaultColumns...)
	}
	if override.Output != nil && len(override.Output.ColumnLabels) > 0 {
		spec.Output.ColumnLabels = copyStringMap(override.Output.ColumnLabels)
	}
	if override.Output != nil && len(override.Output.ColumnFormats) > 0 {
		spec.Output.ColumnFormats = make(map[string]runtime.ColumnFormat, len(override.Output.ColumnFormats))
		for path, format := range override.Output.ColumnFormats {
			spec.Output.ColumnFormats[path] = runtime.ColumnFormat(format)
		}
	}
	if override.Output != nil && len(override.Output.ColumnAlignments) > 0 {
		spec.Output.ColumnAlignments = copyStringMap(override.Output.ColumnAlignments)
	}
	if override.Output != nil && override.Output.Streaming != nil {
		stream := override.Output.Streaming
		collect := stream.Collect
		policy := &runtime.StreamPolicy{DataFormat: stream.Data, EventNamePath: stream.EventNamePath}
		if collect != nil {
			policy.Collect = &runtime.StreamCollectHint{
				RequireStop: collect.RequireStop,
				StopEvents:  append([]string(nil), collect.StopEvents...),
				PauseEvents: append([]string(nil), collect.PauseEvents...),
				ErrorEvents: append([]string(nil), collect.ErrorEvents...),
				Fields:      make([]runtime.StreamFieldRule, 0, len(collect.Fields)),
			}
			for _, field := range collect.Fields {
				policy.Collect.Fields = append(policy.Collect.Fields, runtime.StreamFieldRule{
					Events: append([]string(nil), field.Events...), From: field.From, Value: field.Value, To: field.To, Reduce: field.Reduce,
				})
			}
		}
		if stream.Live != nil {
			policy.Live = &runtime.StreamLiveHint{Events: append([]string(nil), stream.Live.Events...), From: stream.Live.From}
		}
		spec.Output.Streaming.Policy = policy
	}
}

func runtimeCommandExample(example overlay.Example) runtime.CommandExample {
	var bodyShape json.RawMessage
	if len(example.BodyShape) > 0 {
		bodyShape, _ = json.Marshal(example.BodyShape)
	}
	out := runtime.CommandExample{
		Summary:          example.Summary,
		Command:          example.Command,
		BodyShape:        bodyShape,
		FollowUpCommands: append([]string(nil), example.FollowUpCommands...),
	}
	if example.OutputHints.IDPath != "" || example.OutputHints.ListPath != "" {
		out.OutputHints = &runtime.ExampleOutputHints{
			IDPath:   example.OutputHints.IDPath,
			ListPath: example.OutputHints.ListPath,
		}
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}
