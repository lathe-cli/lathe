package normalize

import (
	"fmt"
	"slices"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

type FlagRename struct {
	OperationID string
	Method      string
	Path        string
	Name        string
	In          string
	From        string
}

var reservedParamFlagNames = map[string]bool{
	"hostname": true,
	"output":   true,
	"insecure": true,
	"debug":    true,
	"help":     true,
}

func resolveReservedParamFlags(spec *runtime.CommandSpec) []FlagRename {
	reserved := reservedParamFlagNames
	used := map[string]bool{}
	for _, param := range spec.Params {
		for _, name := range append([]string{param.Flag}, param.Aliases...) {
			for _, suffix := range []string{"", "-env", "-file", "-stdin"} {
				used[name+suffix] = true
			}
		}
	}
	available := func(name string) bool {
		for _, suffix := range []string{"", "-env", "-file", "-stdin"} {
			if used[name+suffix] || reserved[name+suffix] {
				return false
			}
		}
		return true
	}
	var warnings []FlagRename
	for i := range spec.Params {
		param := &spec.Params[i]
		if param.In == runtime.InBody {
			continue
		}
		names := append([]string{param.Flag}, param.Aliases...)
		for _, name := range names {
			if reserved[name] {
				warnings = append(warnings, FlagRename{OperationID: spec.OperationID, Method: spec.Method, Path: spec.PathTpl, Name: param.Name, In: param.In, From: name})
			}
		}
		if reserved[param.Flag] {
			prefix := param.In
			if prefix == runtime.InFormData {
				prefix = "body"
			}
			base := prefix + "-" + param.Flag
			candidate := base
			for n := 2; !available(candidate); n++ {
				candidate = fmt.Sprintf("%s-%d", base, n)
			}
			param.Flag = candidate
			for _, suffix := range []string{"", "-env", "-file", "-stdin"} {
				used[candidate+suffix] = true
			}
		}
		param.Aliases = slices.DeleteFunc(param.Aliases, func(alias string) bool { return reserved[alias] })
	}
	return warnings
}

func ValidateReservedParamFlags(spec runtime.CommandSpec) error {
	reserved := reservedParamFlagNames
	for _, param := range spec.Params {
		for _, name := range append([]string{param.Flag}, param.Aliases...) {
			if reserved[name] {
				return fmt.Errorf("parameter %q flag %q conflicts with a reserved flag; set params.%s.flag to a different name in the overlay", param.Name, name, param.Name)
			}
		}
	}
	return runtime.ValidateParamFlags(spec.Params)
}
