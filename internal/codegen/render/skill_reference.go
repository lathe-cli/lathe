package render

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func renderModuleReference(manifest *config.Manifest, mod SkillModule, flat bool) string {
	cli := manifest.CLI.Name
	specs := visibleSpecs(mod.Specs)
	var b strings.Builder
	fmt.Fprintf(&b, "# Module `%s`\n\n", moduleName(mod))
	b.WriteString("## Source\n\n")
	if mod.Source != nil {
		fmt.Fprintf(&b, "- Backend: `%s`\n", mod.Source.Backend)
		if mod.Source.DefaultHostname != nil {
			fmt.Fprintf(&b, "- Default hostname: `%s`\n", *mod.Source.DefaultHostname)
		}
		fmt.Fprintf(&b, "- Repository: %s\n", valueOrUnknown(mod.Source.RepoURL))
		fmt.Fprintf(&b, "- Pinned tag: `%s`\n", valueOrUnknown(mod.Source.PinnedTag))
		for _, line := range sourceInputs(mod.Source) {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	if mod.State != nil && mod.State.ResolvedSHA != "" {
		fmt.Fprintf(&b, "- Resolved SHA: `%s`\n", mod.State.ResolvedSHA)
	}
	b.WriteString("\n")
	if len(specs) == 0 {
		b.WriteString("No visible generated commands. Use `commands --include-hidden --json` if hidden commands are relevant.\n")
		return b.String()
	}
	groups := groupSpecs(specs)
	module := moduleName(mod)
	for _, group := range slices.Sorted(maps.Keys(groups)) {
		fmt.Fprintf(&b, "## %s\n\n", group)
		for _, spec := range groups[group] {
			path := commandPath(cli, module, spec, flat)
			fmt.Fprintf(&b, "### `%s`\n\n", strings.Join(path, " "))
			if spec.Short != "" {
				fmt.Fprintf(&b, "- Summary: %s\n", oneLine(spec.Short))
			}
			fmt.Fprintf(&b, "- HTTP: `%s %s`\n", spec.Method, spec.PathTpl)
			fmt.Fprintf(&b, "- Auth: %s\n", authSummary(spec.Security))
			fmt.Fprintf(&b, "- Body: %s\n", bodySummary(spec.RequestBody))
			writeShortcuts(&b, cli, spec)
			if len(spec.Params) == 0 {
				b.WriteString("- Flags: none\n")
			} else {
				b.WriteString("- Flags:\n")
				position := 0
				for _, p := range spec.Params {
					req := ""
					if p.Required {
						req = ", required"
					}
					def := ""
					if p.Default != "" {
						def = fmt.Sprintf(", default `%s`", p.Default)
					}
					format := ""
					if p.Format != "" {
						format = ", " + p.Format
					}
					enum := ""
					if len(p.Enum) > 0 {
						enum = ", one of: " + strings.Join(p.Enum, "|")
					}
					if len(p.ItemEnum) > 0 {
						enum = ", items one of: " + strings.Join(p.ItemEnum, "|")
					}
					deprecated := ""
					if p.Deprecated {
						deprecated = ", deprecated"
					}
					if p.Context != "" {
						info := manifest.Contexts[p.Context]
						contextSource := ", context `" + p.Context + "`"
						if info.Env != "" {
							contextSource += " via `" + info.Env + "`"
						}
						deprecated += contextSource
					}
					input := fmt.Sprintf("`--%s`", p.Flag)
					if p.Argument != "" {
						position++
						input = fmt.Sprintf("argument %d `[%s]` or `--%s`", position, p.Argument, p.Flag)
					}
					fmt.Fprintf(&b, "  - %s (%s%s%s%s%s%s): %s\n", input, p.In, req, def, format, enum, deprecated, oneLine(stripHelpMeta(p.Help)))
				}

			}
			if out := outputSummary(spec.Output); out != "" {
				fmt.Fprintf(&b, "- Output: %s\n", out)
			}
			writeOperationContext(&b, spec)
			writeExamples(&b, cli, module, spec, flat)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeShortcuts(b *strings.Builder, cli string, spec runtime.CommandSpec) {
	if len(spec.Shortcuts) == 0 {
		return
	}
	b.WriteString("- Shortcuts:\n")
	for _, shortcut := range spec.Shortcuts {
		preset := shortcutPreset(spec, shortcut)
		if preset == "" {
			fmt.Fprintf(b, "  - `%s %s`\n", cli, shortcut.Use)
			continue
		}
		fmt.Fprintf(b, "  - `%s %s` preset %s\n", cli, shortcut.Use, preset)
	}
}

func shortcutPreset(spec runtime.CommandSpec, shortcut runtime.CommandShortcut) string {
	if len(shortcut.Params) == 0 {
		return ""
	}
	flags := make(map[string]string, len(spec.Params)*2)
	for _, param := range spec.Params {
		flags[param.Name] = param.Flag
		flags[param.Flag] = param.Flag
	}
	keys := slices.Sorted(maps.Keys(shortcut.Params))
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		flag := flags[key]
		if flag == "" {
			flag = key
		}
		parts = append(parts, fmt.Sprintf("`--%s=%s`", flag, shortcut.Params[key]))
	}
	return strings.Join(parts, ", ")
}

func commandExample(example, cli, module string, spec runtime.CommandSpec, flat bool) string {
	newPath := strings.Join(commandPath(cli, module, spec, true), " ")
	oldPaths := []string{strings.Join(legacyCommandPath(cli, module, spec, flat), " ")}
	if !flat {
		newPath = strings.Join(commandPath(cli, module, spec, false), " ")
	} else {
		oldPaths = append([]string{
			strings.Join(commandPath(cli, module, spec, false), " "),
			strings.Join(legacyCommandPath(cli, module, spec, false), " "),
		}, oldPaths...)
	}
	for _, oldPath := range oldPaths {
		if oldPath != newPath {
			example = strings.ReplaceAll(example, oldPath, newPath)
		}
	}
	return example
}

func writeOperationContext(b *strings.Builder, spec runtime.CommandSpec) {
	writeStringList(b, "Notes", spec.Notes)
	writeStringList(b, "Prerequisites", spec.Prerequisites)
	if len(spec.SearchTerms) > 0 {
		terms := make([]string, 0, len(spec.SearchTerms))
		for _, term := range spec.SearchTerms {
			terms = append(terms, "`"+strings.ReplaceAll(oneLine(term), "`", "'")+"`")
		}
		fmt.Fprintf(b, "- Search terms: %s\n", strings.Join(terms, ", "))
	}
	if spec.SetContext != nil {
		param := spec.SetContext.Param
		if index, count := paramByNameOrFlag(spec.Params, param); count == 1 {
			param = spec.Params[index].Flag
		}
		fmt.Fprintf(b, "- Sets context `%s` from parameter `%s` after success.\n", spec.SetContext.Name, strings.ReplaceAll(oneLine(param), "`", "'"))
	}
	if len(spec.KnownErrors) == 0 {
		return
	}
	b.WriteString("- Known errors:\n")
	for _, err := range spec.KnownErrors {
		label := "unspecified status"
		if err.Status != 0 {
			label = fmt.Sprintf("HTTP %d", err.Status)
		}
		cause := oneLine(err.Cause)
		if cause == "" {
			fmt.Fprintf(b, "  - %s\n", label)
			continue
		}
		fmt.Fprintf(b, "  - %s: %s\n", label, cause)
	}
}

func writeStringList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s:\n", label)
	for _, value := range values {
		fmt.Fprintf(b, "  - %s\n", oneLine(value))
	}
}

func writeExample(b *strings.Builder, example string) {
	text := strings.TrimRight(example, "\n")
	if strings.Contains(text, "\n") {
		fence := markdownFence(text)
		fmt.Fprintf(b, "- Example:\n\n%s\n%s\n%s\n", fence, text, fence)
		return
	}
	fmt.Fprintf(b, "- Example: `%s`\n", strings.ReplaceAll(oneLine(text), "`", "'"))
}

func writeExamples(b *strings.Builder, cli, module string, spec runtime.CommandSpec, flat bool) {
	examples := spec.Examples
	if len(examples) == 0 && spec.Example != "" {
		examples = []runtime.CommandExample{{Command: spec.Example}}
	}
	if len(examples) == 0 {
		return
	}
	if len(examples) == 1 {
		example := examples[0]
		if example.Summary == "" && example.Command != "" && len(example.BodyShape) == 0 && example.OutputHints == nil && len(example.FollowUpCommands) == 0 {
			writeExample(b, commandExample(example.Command, cli, module, spec, flat))
			return
		}
	}
	b.WriteString("- Examples:\n")
	for _, example := range examples {
		summary := oneLine(example.Summary)
		if summary == "" {
			summary = "Example"
		}
		fmt.Fprintf(b, "  - %s\n", summary)
		if example.Command != "" {
			fmt.Fprintf(b, "    Command: `%s`\n", strings.ReplaceAll(oneLine(commandExample(example.Command, cli, module, spec, flat)), "`", "'"))
		}
		if len(example.BodyShape) > 0 {
			fmt.Fprintf(b, "    Body shape: `%s`\n", strings.ReplaceAll(oneLine(string(example.BodyShape)), "`", "'"))
		}
		if example.OutputHints != nil {
			if example.OutputHints.IDPath != "" {
				fmt.Fprintf(b, "    Output ID path: `%s`\n", example.OutputHints.IDPath)
			}
			if example.OutputHints.ListPath != "" {
				fmt.Fprintf(b, "    Output list path: `%s`\n", example.OutputHints.ListPath)
			}
		}
		if len(example.FollowUpCommands) > 0 {
			b.WriteString("    Follow-up commands:\n")
			for _, command := range example.FollowUpCommands {
				fmt.Fprintf(b, "      - `%s`\n", strings.ReplaceAll(oneLine(commandExample(command, cli, module, spec, flat)), "`", "'"))
			}
		}
	}
}

func markdownFence(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence
}

func visibleSpecs(specs []runtime.CommandSpec) []runtime.CommandSpec {
	out := make([]runtime.CommandSpec, 0, len(specs))
	for _, spec := range specs {
		if !spec.Hidden {
			out = append(out, spec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Group == out[j].Group {
			return out[i].Use < out[j].Use
		}
		return out[i].Group < out[j].Group
	})
	return out
}

func groupSpecs(specs []runtime.CommandSpec) map[string][]runtime.CommandSpec {
	out := map[string][]runtime.CommandSpec{}
	for _, spec := range specs {
		group := spec.Group
		if group == "" {
			group = "Commands"
		}
		out[group] = append(out[group], spec)
	}
	return out
}

func moduleName(mod SkillModule) string {
	if mod.Source != nil && mod.Source.DisplayName != "" {
		return mod.Source.DisplayName
	}
	if mod.Source != nil && mod.Source.Name != "" {
		return mod.Source.Name
	}
	if mod.State != nil && mod.State.Source != "" {
		return mod.State.Source
	}
	return "module"
}

func sourceInputs(src *sourceconfig.Source) []string {
	switch src.Backend {
	case sourceconfig.BackendSwagger:
		return []string{"Files: `" + strings.Join(src.Swagger.Files, "`, `") + "`"}
	case sourceconfig.BackendOpenAPI3:
		return []string{"Files: `" + strings.Join(src.OpenAPI3.Files, "`, `") + "`"}
	case sourceconfig.BackendProto:
		lines := []string{"Entries: `" + strings.Join(src.Proto.Entries, "`, `") + "`"}
		if len(src.Proto.ImportRoots) > 0 {
			lines = append(lines, "Import roots: `"+strings.Join(src.Proto.ImportRoots, "`, `")+"`")
		}
		if len(src.Proto.Staging) > 0 {
			lines = append(lines, fmt.Sprintf("Staging entries: `%d`", len(src.Proto.Staging)))
		}
		return lines
	case sourceconfig.BackendGraphQL:
		if src.GraphQL == nil {
			return nil
		}
		lines := []string{"Schema: `" + src.GraphQL.Schema + "`"}
		if src.GraphQL.Expose != nil {
			if len(src.GraphQL.Expose.Queries) > 0 {
				lines = append(lines, "Expose queries: `"+strings.Join(src.GraphQL.Expose.Queries, "`, `")+"`")
			}
			if len(src.GraphQL.Expose.Mutations) > 0 {
				lines = append(lines, "Expose mutations: `"+strings.Join(src.GraphQL.Expose.Mutations, "`, `")+"`")
			}
		}
		if len(src.GraphQL.Groups) > 0 {
			lines = append(lines, fmt.Sprintf("Group policies: `%d`", len(src.GraphQL.Groups)))
		}
		if len(src.GraphQL.Output) > 0 {
			lines = append(lines, fmt.Sprintf("Output policies: `%d`", len(src.GraphQL.Output)))
		}
		if src.GraphQL.Selection != nil {
			parts := make([]string, 0, 2)
			if src.GraphQL.Selection.MaxDepth != nil {
				parts = append(parts, fmt.Sprintf("max depth `%d`", *src.GraphQL.Selection.MaxDepth))
			}
			if len(src.GraphQL.Selection.Prune) > 0 {
				parts = append(parts, fmt.Sprintf("prune rules `%d`", len(src.GraphQL.Selection.Prune)))
			}
			if len(parts) > 0 {
				lines = append(lines, "Selection policy: "+strings.Join(parts, "; "))
			}
		}
		return lines
	default:
		return nil
	}
}

func commandPath(cli, module string, spec runtime.CommandSpec, flat bool) []string {
	path := []string{cli}
	if !flat {
		path = append(path, module)
	}
	if group := rootCommandName(spec.Group); group != "" {
		path = append(path, group)
	}
	return append(path, spec.Use)
}

func legacyCommandPath(cli, module string, spec runtime.CommandSpec, flat bool) []string {
	path := []string{cli}
	if !flat {
		path = append(path, module)
	}
	if spec.Group != "" {
		path = append(path, strings.ToLower(spec.Group))
	}
	return append(path, spec.Use)
}

func authSummary(security *runtime.SecurityHint) string {
	if security != nil && security.Public {
		return "public"
	}
	if security != nil && len(security.Scopes) > 0 {
		return "required; scopes: `" + strings.Join(security.Scopes, "`, `") + "`"
	}
	return "required"
}

func bodySummary(body *runtime.RequestBody) string {
	if body == nil {
		return "none"
	}
	state := "optional"
	if body.Required {
		state = "required"
	}
	if body.Template != "" {
		merge := body.MergePath
		if merge == "" {
			merge = "the document root"
		}
		return state + "; templated body, set inputs under `" + merge + "` with --set/--set-str/--file"
	}
	if body.MediaType != "" {
		state += "; media type `" + body.MediaType + "`"
	}
	if body.RuntimeSchema != nil {
		state += "; runtime schema preflight"
	}
	return state
}

func outputSummary(out runtime.OutputHints) string {
	parts := make([]string, 0, 5)
	if out.ListPath != "" {
		parts = append(parts, "list path `"+out.ListPath+"`")
	}
	if len(out.DefaultColumns) > 0 {
		parts = append(parts, "columns `"+strings.Join(out.DefaultColumns, "`, `")+"`")
	}
	if out.ResponseMediaType != "" {
		parts = append(parts, "response media `"+out.ResponseMediaType+"`")
	}
	if out.Pagination != nil {
		parts = append(parts, "pagination `"+out.Pagination.Strategy+"`")
	}
	if out.Streaming != nil {
		streaming := "streaming `" + out.Streaming.Strategy + "`"
		if out.Streaming.Policy != nil {
			streaming += ", collected"
			if out.Streaming.Policy.Live != nil {
				streaming += ", live `--stream`"
			}
		}
		parts = append(parts, streaming)
	}
	return strings.Join(parts, "; ")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func stripHelpMeta(s string) string {
	if i := strings.LastIndex(s, " ("); i >= 0 && strings.HasSuffix(s, ")") {
		return s[:i]
	}
	return s
}

func valueOrUnknown(s string) string {
	if s == "" {
		return "`unknown`"
	}
	return s
}
