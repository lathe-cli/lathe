package runtime

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const ModuleGroupID = "modules"

func AssertSchema(generated int) error {
	if generated != SchemaVersion {
		return fmt.Errorf(
			"lathe schema mismatch: generated code uses schema %d but runtime expects %d — re-run codegen",
			generated, SchemaVersion,
		)
	}
	return nil
}

const completionRootName = "completion"

func Build(root *cobra.Command, service string, specs []CommandSpec) error {
	if findChildCommand(root, service) != nil || service == completionRootName {
		return fmt.Errorf("module command %q conflicts with an existing root command", service)
	}
	groups, err := buildGroups(service, specs)
	if err != nil {
		return err
	}
	svc := helpCommand(service, service+" API")
	svc.GroupID = ModuleGroupID
	for _, group := range groups {
		svc.AddCommand(group)
	}
	if err := ValidateShortcuts(specs, rootCommandNames(root, svc)); err != nil {
		return err
	}
	root.AddCommand(svc)
	return mountShortcuts(root, specs)
}

func BuildFlat(root *cobra.Command, service string, specs []CommandSpec) error {
	groups, err := buildGroups(service, specs)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, group := range groups {
		group.GroupID = ModuleGroupID
		name := group.Name()
		if seen[name] {
			return fmt.Errorf("flat mount command %q conflicts with another generated command", name)
		}
		seen[name] = true
		if findChildCommand(root, name) != nil || name == completionRootName {
			return fmt.Errorf("flat mount command %q conflicts with existing root command", name)
		}
	}
	if err := ValidateShortcuts(specs, rootCommandNames(root, groups...)); err != nil {
		return err
	}
	root.AddCommand(groups...)
	return mountShortcuts(root, specs)
}

func buildGroups(service string, specs []CommandSpec) ([]*cobra.Command, error) {
	groups := map[string]*cobra.Command{}
	commandPaths := map[string]*cobra.Command{}
	ordered := make([]*cobra.Command, 0)
	for i := range specs {
		s := specs[i]
		if err := ValidateParamFlags(s.Params); err != nil {
			return nil, fmt.Errorf("command %q: %w", s.Use, err)
		}
		groupShort := s.GroupShort
		if groupShort == "" {
			groupShort = s.Group + " operations"
		}
		g, ok := groups[s.Group]
		if !ok {
			g = helpCommand(strings.ToLower(s.Group), groupShort)
			groups[s.Group] = g
			ordered = append(ordered, g)
		} else if g.Short != groupShort {
			return nil, fmt.Errorf("group %q has conflicting descriptions", s.Group)
		}
		c := buildCmd(s)
		for _, name := range append([]string{c.Name()}, c.Aliases...) {
			path := g.Name() + " " + name
			if existing := commandPaths[path]; existing != nil && existing != c {
				return nil, fmt.Errorf("command name or alias %q conflicts between %q and %q in group %q", name, existing.Name(), c.Name(), g.Name())
			}
			commandPaths[path] = c
		}
		AttachCatalogCommand(c, service, s)
		g.AddCommand(c)
	}
	return ordered, nil
}

func helpCommand(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
}

func mountShortcuts(root *cobra.Command, specs []CommandSpec) error {
	for _, spec := range specs {
		for _, shortcut := range spec.Shortcuts {
			target, err := shortcutSpec(spec, shortcut)
			if err != nil {
				return err
			}
			cmd := buildCmd(target)
			cmd.GroupID = ModuleGroupID
			root.AddCommand(cmd)
		}
	}
	return nil
}

func ValidateShortcuts(specs []CommandSpec, rootNames []string) error {
	roots := map[string]bool{}
	for _, name := range rootNames {
		if name != "" {
			roots[name] = true
		}
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		for _, shortcut := range spec.Shortcuts {
			name, err := shortcutName(shortcut.Use, spec.Use)
			if err != nil {
				return err
			}
			if roots[name] {
				return fmt.Errorf("shortcut %q conflicts with root command", name)
			}
			if seen[name] {
				return fmt.Errorf("shortcut %q conflicts with another shortcut", name)
			}
			seen[name] = true
			if _, err := shortcutSpec(spec, shortcut); err != nil {
				return err
			}
		}
	}
	return nil
}

func rootCommandNames(root *cobra.Command, planned ...*cobra.Command) []string {
	names := []string{completionRootName}
	for _, cmd := range root.Commands() {
		names = append(names, cmd.Name())
		names = append(names, cmd.Aliases...)
	}
	for _, cmd := range planned {
		names = append(names, cmd.Name())
		names = append(names, cmd.Aliases...)
	}
	return names
}

func shortcutSpec(spec CommandSpec, shortcut CommandShortcut) (CommandSpec, error) {
	name, err := shortcutName(shortcut.Use, spec.Use)
	if err != nil {
		return CommandSpec{}, err
	}
	target := spec
	target.Use = name
	target.Aliases = nil
	target.Shortcuts = nil
	target.Params = append([]ParamSpec(nil), spec.Params...)
	set := map[int]string{}
	for key, value := range shortcut.Params {
		i := shortcutParamIndex(spec, key)
		if i < 0 {
			return CommandSpec{}, fmt.Errorf("shortcut %q param %q does not match command %q", name, key, spec.Use)
		}
		if prior, ok := set[i]; ok && prior != key {
			return CommandSpec{}, fmt.Errorf("shortcut %q sets param %q more than once", name, spec.Params[i].Name)
		}
		if err := validateShortcutParamValue(spec.Params[i], value); err != nil {
			return CommandSpec{}, fmt.Errorf("shortcut %q param %q: %w", name, key, err)
		}
		set[i] = key
		target.Params[i].Default = value
		target.Params[i].Required = false
		target.Params[i].Context = ""
	}
	return target, nil
}

func shortcutName(use string, target string) (string, error) {
	name := strings.TrimSpace(use)
	if name == "" || name != use || len(strings.Fields(name)) != 1 {
		return "", fmt.Errorf("shortcut for command %q must be a single command name", target)
	}
	return name, nil
}

func shortcutParamIndex(spec CommandSpec, key string) int {
	for i, param := range spec.Params {
		if key == param.Name || key == param.Flag {
			return i
		}
	}
	return -1
}

func validateShortcutParamValue(p ParamSpec, value string) error {
	if strings.HasPrefix(p.GoType, "[]") {
		return fmt.Errorf("repeated params are not supported")
	}
	switch {
	case (p.In == InPath || p.In == InHeader) && p.GoType != "" && p.GoType != "string":
		return fmt.Errorf("type %s is not supported for %s params", p.GoType, p.In)
	case p.In == InFormData && p.GoType == "float64":
		return fmt.Errorf("type %s is not supported for %s params", p.GoType, p.In)
	case p.In != InVariable && p.In != InBody && p.GoType == "float64":
		return fmt.Errorf("type %s is not supported for %s params", p.GoType, p.In)
	}
	switch p.GoType {
	case "int64":
		_, err := strconv.ParseInt(value, 10, 64)
		return err
	case "float64":
		_, err := strconv.ParseFloat(value, 64)
		return err
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("must be true or false")
		}
	}
	return nil
}
