package runtime

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const catalogCommandAnnotation = "lathe.catalog.command"

const catalogCapabilitiesAnnotation = "lathe.catalog.capabilities"

const catalogDryRunWiredAnnotation = "lathe.dry_run.wired"

func AttachCatalogCommand(cmd *cobra.Command, service string, spec CommandSpec) {
	entry := catalogCommand(service, spec, nil)
	if flag := WiredDryRunFlag(cmd); flag != "" {
		entry.DryRun = &CatalogDryRun{Mode: DryRunHTTPPreview, Flag: flag}
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[catalogCommandAnnotation] = string(raw)
}

func WiredDryRunFlag(cmd *cobra.Command) string {
	if cmd == nil || cmd.Annotations == nil {
		return ""
	}
	return cmd.Annotations[catalogDryRunWiredAnnotation]
}

func AttachCatalogWorkflowCommand(cmd *cobra.Command, spec WorkflowSpec) {
	entry := catalogWorkflowCommand(spec, nil)
	raw, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[catalogCommandAnnotation] = string(raw)
}

func AttachCapability(root *cobra.Command, capability string) {
	if root == nil || capability == "" {
		return
	}
	values := append(capabilitiesFromAnnotation(root), capability)
	values = normalizeCapabilities(values)
	if root.Annotations == nil {
		root.Annotations = map[string]string{}
	}
	root.Annotations[catalogCapabilitiesAnnotation] = strings.Join(values, ",")
}

func HasCapability(root *cobra.Command, capability string) bool {
	for _, value := range Capabilities(root) {
		if value == capability {
			return true
		}
	}
	return false
}

func Capabilities(root *cobra.Command) []string {
	return normalizeCapabilities(capabilitiesFromAnnotation(root))
}

func BuildCatalog(root *cobra.Command, opts CatalogOptions) Catalog {
	if opts.CLIName == "" {
		opts.CLIName = root.Use
	}
	capabilities := normalizeCapabilities(append(append([]string(nil), opts.Capabilities...), Capabilities(root)...))
	commands := make([]CatalogCommand, 0)
	walkCatalog(root, nil, opts, &commands)
	sort.Slice(commands, func(i, j int) bool {
		return slices.Compare(commands[i].Path, commands[j].Path) < 0
	})
	return Catalog{
		CatalogSchemaVersion: CatalogSchemaVersion,
		CLI:                  CatalogCLI{Name: opts.CLIName, Version: opts.CLIVersion, Capabilities: capabilities},
		Output:               CatalogOutputFormats{DefaultFormat: "table", Formats: FormatterNames()},
		Commands:             commands,
	}
}

func capabilitiesFromAnnotation(root *cobra.Command) []string {
	if root == nil || root.Annotations == nil {
		return nil
	}
	raw := root.Annotations[catalogCapabilitiesAnnotation]
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func normalizeCapabilities(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func FindCatalogCommand(root *cobra.Command, path []string, opts CatalogOptions) (CatalogCommand, bool) {
	cur := root
	canonical := make([]string, 0, len(path))
	for _, segment := range path {
		child := findChildCommand(cur, segment)
		if child == nil {
			return findCatalogShortcut(root, path, opts)
		}
		canonical = append(canonical, child.Name())
		cur = child
	}
	cmd, ok := catalogCommandFromAnnotation(cur, canonical)
	if !ok || (!opts.IncludeHidden && cmd.Hidden) {
		return findCatalogShortcut(root, path, opts)
	}
	return cmd, true
}

func findCatalogShortcut(root *cobra.Command, path []string, opts CatalogOptions) (CatalogCommand, bool) {
	if len(path) != 1 {
		return CatalogCommand{}, false
	}
	for _, cmd := range BuildCatalog(root, opts).Commands {
		for _, shortcut := range cmd.Shortcuts {
			if shortcut.Use == path[0] {
				return cmd, true
			}
		}
	}
	return CatalogCommand{}, false
}

func walkCatalog(cmd *cobra.Command, path []string, opts CatalogOptions, out *[]CatalogCommand) {
	path = slices.Clone(path)
	if cmd != nil && cmd.Parent() != nil {
		path = append(path, cmd.Name())
	}
	if cc, ok := catalogCommandFromAnnotation(cmd, path); ok {
		if opts.IncludeHidden || !cc.Hidden {
			*out = append(*out, cc)
		}
	}
	for _, child := range cmd.Commands() {
		walkCatalog(child, path, opts, out)
	}
}

func catalogCommandFromAnnotation(cmd *cobra.Command, path []string) (CatalogCommand, bool) {
	if cmd == nil || cmd.Annotations == nil {
		return CatalogCommand{}, false
	}
	raw := cmd.Annotations[catalogCommandAnnotation]
	if raw == "" {
		return CatalogCommand{}, false
	}
	var entry CatalogCommand
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return CatalogCommand{}, false
	}
	entry.Path = slices.Clone(path)
	return entry, true
}

func findChildCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name || child.HasAlias(name) {
			return child
		}
	}
	return nil
}
