package render

import (
	"fmt"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func ResolveFlatCommandPath(policy string, moduleCount int, specs []runtime.CommandSpec) (bool, error) {
	if err := validateCommandPaths(specs); err != nil {
		return false, err
	}
	if policy == "" {
		policy = config.CommandPathAuto
	}
	if moduleCount != 1 {
		if policy == config.CommandPathFlat {
			return false, fmt.Errorf("cli.command_path=flat requires exactly one source module")
		}
		return false, nil
	}
	switch policy {
	case config.CommandPathNamespaced:
		return false, nil
	case config.CommandPathFlat:
		if conflict, ok := flatPathConflict(specs); ok {
			return false, fmt.Errorf("cli.command_path=flat conflicts with command %q", conflict)
		}
		return true, nil
	case config.CommandPathAuto:
		_, conflict := flatPathConflict(specs)
		return !conflict, nil
	default:
		return false, fmt.Errorf("unknown cli.command_path %q", policy)
	}
}

func RewriteCommandExamples(cli, module string, specs []runtime.CommandSpec, flat bool) []runtime.CommandSpec {
	if cli == "" {
		return specs
	}
	rewritten := make([]runtime.CommandSpec, 0, len(specs))
	for _, spec := range specs {
		next := cloneCommandSpec(spec)
		if next.Example != "" {
			next.Example = commandExample(next.Example, cli, module, next, flat)
		}
		for i := range next.Examples {
			if next.Examples[i].Command != "" {
				next.Examples[i].Command = commandExample(next.Examples[i].Command, cli, module, next, flat)
			}
			for j := range next.Examples[i].FollowUpCommands {
				next.Examples[i].FollowUpCommands[j] = commandExample(next.Examples[i].FollowUpCommands[j], cli, module, next, flat)
			}
		}
		rewritten = append(rewritten, next)
	}
	return rewritten
}

func flatPathConflict(specs []runtime.CommandSpec) (string, bool) {
	seen := map[string]string{}
	for _, spec := range specs {
		name := rootCommandName(spec.Group)
		if name == "" {
			continue
		}
		if reservedRootCommands[name] {
			return name, true
		}
		if group, ok := seen[name]; ok && group != spec.Group {
			return name, true
		}
		seen[name] = spec.Group
	}
	return "", false
}

func validateCommandPaths(specs []runtime.CommandSpec) error {
	type commandName struct {
		spec  runtime.CommandSpec
		alias bool
		index int
	}
	seen := map[string]commandName{}
	for specIndex, spec := range specs {
		group := rootCommandName(spec.Group)
		use := commandUseName(spec.Use)
		if group == "" || use == "" {
			return fmt.Errorf("command %q has empty generated path", commandIdentity(spec))
		}
		names := append([]string{use}, spec.Aliases...)
		for i, raw := range names {
			name := commandUseName(raw)
			if name == "" {
				return fmt.Errorf("command %q has empty alias", commandIdentity(spec))
			}
			cmdPath := group + " " + name
			if prev, ok := seen[cmdPath]; ok {
				if prev.index == specIndex {
					continue
				}
				kind := "command path"
				if i > 0 || prev.alias {
					kind = "command alias path"
				}
				return fmt.Errorf("%s %q conflicts between %s and %s", kind, cmdPath, commandDebugIdentity(prev.spec), commandDebugIdentity(spec))
			}
			seen[cmdPath] = commandName{spec: spec, alias: i > 0, index: specIndex}
		}
	}
	return nil
}

func commandUseName(use string) string {
	fields := strings.Fields(use)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func commandIdentity(spec runtime.CommandSpec) string {
	if spec.OperationID != "" {
		return spec.OperationID
	}
	if spec.Group != "" || spec.Use != "" {
		return strings.TrimSpace(spec.Group + " " + spec.Use)
	}
	return spec.PathTpl
}

func commandDebugIdentity(spec runtime.CommandSpec) string {
	var parts []string
	if spec.OperationID != "" {
		parts = append(parts, fmt.Sprintf("operationId=%q", spec.OperationID))
	}
	if spec.Method != "" || spec.PathTpl != "" {
		parts = append(parts, fmt.Sprintf("http=%q", strings.TrimSpace(spec.Method+" "+spec.PathTpl)))
	}
	if spec.Group != "" || spec.Use != "" {
		parts = append(parts, fmt.Sprintf("command=%q", strings.TrimSpace(spec.Group+" "+spec.Use)))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%q", commandIdentity(spec))
	}
	return strings.Join(parts, ", ")
}

func rootCommandName(use string) string {
	fields := strings.Fields(strings.ToLower(use))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

var reservedRootCommands = map[string]bool{
	"__lathe":    true,
	"auth":       true,
	"commands":   true,
	"completion": true,
	"help":       true,
	"login":      true,
	"search":     true,
	"skill":      true,
	"update":     true,
}

func ValidateModuleNames(names []string) error {
	seen := map[string]bool{}
	for _, raw := range names {
		name := rootCommandName(raw)
		if name == "" {
			return fmt.Errorf("module name %q has empty generated root command", raw)
		}
		if reservedRootCommands[name] {
			return fmt.Errorf("module name %q conflicts with a reserved root command", raw)
		}
		if seen[name] {
			return fmt.Errorf("module name %q is mounted more than once", raw)
		}
		seen[name] = true
	}
	return nil
}

func ValidateShortcuts(moduleNames []string, specs []runtime.CommandSpec, flat bool) error {
	rootNames := make([]string, 0, len(reservedRootCommands)+len(moduleNames)+len(specs))
	for name := range reservedRootCommands {
		rootNames = append(rootNames, name)
	}
	if flat {
		seen := map[string]bool{}
		for _, spec := range specs {
			name := rootCommandName(spec.Group)
			if !seen[name] {
				rootNames = append(rootNames, name)
				seen[name] = true
			}
		}
		return runtime.ValidateShortcuts(specs, rootNames)
	}
	rootNames = append(rootNames, moduleNames...)
	return runtime.ValidateShortcuts(specs, rootNames)
}
