package lathe

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/diagnostic"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const (
	snipCommandName = "snip"
	maxSnipNameLen  = 64
)

var reservedSnipNames = map[string]bool{
	"delete": true,
	"help":   true,
	"list":   true,
	"ls":     true,
	"rm":     true,
	"run":    true,
	"set":    true,
}

type snipItem struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

type snipListOutput struct {
	Snips []snipItem `json:"snips"`
}

func snipCmd(m *config.Manifest) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "snip <name> [args...]",
		Short:              "Manage and run saved command snips",
		Long:               "Save personal command snips in snips.yml and run them by name.\nUse $1, $2, and higher positional placeholders for values that change per run.\nAppend extra flags when you run a snip to override saved scalar or --set values.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, rawArgs []string) error {
			return executeNamedSnip(cmd, m, rawArgs, false)
		},
	}
	cmd.AddCommand(
		snipSetCmd(m),
		snipListCmd(m),
		snipDeleteCmd(m),
		snipRunCmd(m),
	)
	baseHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		baseHelp(c, args)
		if c != cmd {
			return
		}
		snips, err := config.LoadSnips()
		if err != nil {
			return
		}
		names := snips.Names()
		if len(names) == 0 {
			return
		}
		w := c.OutOrStdout()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Saved Snips:")
		width := 0
		for _, name := range names {
			if len(name) > width {
				width = len(name)
			}
		}
		for _, name := range names {
			entry, _ := snips.Get(name)
			summary := sanitizeOneLine(entry.Command)
			if entry.Description != "" {
				summary = sanitizeOneLine(entry.Description) + " (" + summary + ")"
			}
			fmt.Fprintf(w, "  %-*s  %s\n", width, sanitizeOneLine(name), summary)
		}
	})
	return cmd
}

func snipSetCmd(m *config.Manifest) *cobra.Command {
	var description string
	cliName := diagnostic.Identifier(m.CLI.Name)
	cmd := &cobra.Command{
		Use:   "set <name> <command...>",
		Short: "Save or update a command snip",
		Example: fmt.Sprintf(
			"  %[1]s snip set active-users \"users list --status active --limit 20\" -d \"Active users\"\n  %[1]s snip set get-user \"users get --id $1\"\n  %[1]s snip set create-user -- users create --set role=member",
			cliName,
		),
		Args: runtime.UsageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSnipOutputFormat(cmd); err != nil {
				return err
			}
			name := args[0]
			if err := validateSnipName(cmd, name); err != nil {
				return err
			}
			var tokens []string
			if cmd.ArgsLenAtDash() < 0 && len(args) == 2 {
				var err error
				tokens, err = splitSnipArgs(args[1])
				if err != nil {
					return runtime.UsageError(cmd, runtime.WithUsageDetail(err, err.Error()))
				}
			} else {
				tokens = append([]string(nil), args[1:]...)
			}
			if len(tokens) == 0 {
				return runtime.UsageError(cmd, runtime.WithUsageDetail(
					errors.New("empty snip command"),
					"snip command must not be empty",
				))
			}
			if _, err := validatePlaceholders(tokens); err != nil {
				return runtime.UsageError(cmd, runtime.WithUsageDetail(err, err.Error()))
			}
			if err := validateSnipTarget(cmd, cmd.Root(), m, tokens); err != nil {
				return err
			}
			canonical := formatSnipCommand(tokens)
			desc := strings.TrimSpace(description)
			if err := config.MutateSnips(cmd.Context(), func(snips *config.Snips) error {
				snips.Set(name, config.SnipEntry{
					Command:     canonical,
					Description: desc,
				})
				return nil
			}); err != nil {
				return err
			}
			return writeSnipOutput(cmd, snipItem{
				Name:        name,
				Command:     canonical,
				Description: desc,
			}, runtime.OutputHints{})
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "Short note for this snip")
	return cmd
}

func snipListCmd(m *config.Manifest) *cobra.Command {
	cliName := diagnostic.Identifier(m.CLI.Name)
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List saved command snips",
		Args:    runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateSnipOutputFormat(cmd); err != nil {
				return err
			}
			snips, err := config.LoadSnips()
			if err != nil {
				return err
			}
			names := snips.Names()
			out := snipListOutput{Snips: make([]snipItem, 0, len(names))}
			for _, name := range names {
				entry, _ := snips.Get(name)
				out.Snips = append(out.Snips, snipItem{
					Name:        name,
					Command:     entry.Command,
					Description: entry.Description,
				})
			}
			format := rootOutputFormat(cmd)
			if format == "table" && len(out.Snips) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No saved snips yet. Run `%s snip set <name> \"<command>\"` to save one.\n", cliName)
				return nil
			}
			return writeSnipOutput(cmd, out, runtime.OutputHints{
				ListPath:       "snips",
				DefaultColumns: []string{"name", "command", "description"},
			})
		},
	}
}

func snipDeleteCmd(_ *config.Manifest) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a saved command snip",
		Args:    runtime.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSnipOutputFormat(cmd); err != nil {
				return err
			}
			name := args[0]
			if err := config.MutateSnips(cmd.Context(), func(snips *config.Snips) error {
				if !snips.Delete(name) {
					return snipNotFoundError(cmd)
				}
				return nil
			}); err != nil {
				return err
			}
			return writeSnipOutput(cmd, map[string]string{
				"name":   name,
				"status": "deleted",
			}, runtime.OutputHints{})
		},
	}
}

func snipRunCmd(m *config.Manifest) *cobra.Command {
	return &cobra.Command{
		Use:                "run <name> [args...]",
		Short:              "Run a saved command snip",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, rawArgs []string) error {
			return executeNamedSnip(cmd, m, rawArgs, true)
		},
	}
}

func executeNamedSnip(cmd *cobra.Command, m *config.Manifest, rawArgs []string, requireName bool) error {
	root := cmd.Root()
	leadingFlags, snipName, invokeArgs, helpRequested, err := splitLeadingRootFlags(root, rawArgs)
	if f := errorOutputFormat(leadingFlags); f == "json" || f == "yaml" {
		_ = root.PersistentFlags().Set("output", f)
	}
	if err != nil {
		return runtime.UsageError(cmd, err)
	}
	if helpRequested {
		return cmd.Help()
	}
	if err := validateLeadingOutputFlag(cmd, leadingFlags); err != nil {
		return err
	}
	if snipName == "" {
		if !requireName {
			return cmd.Help()
		}
		return runtime.UsageError(cmd, runtime.WithUsageDetail(
			errors.New("missing snip name"),
			"snip name is required",
		))
	}
	if !requireName && snipName == "help" {
		if len(invokeArgs) == 0 {
			return cmd.Help()
		}
		sub := findSubcommand(cmd, invokeArgs[0])
		if sub == nil {
			return runtime.UsageError(cmd, errors.New("unknown help topic"))
		}
		return sub.Help()
	}

	snips, err := config.LoadSnips()
	if err != nil {
		return err
	}
	entry, ok := snips.Get(snipName)
	if !ok {
		return snipNotFoundError(cmd)
	}
	savedTokens, err := splitSnipArgs(entry.Command)
	if err != nil || len(savedTokens) == 0 {
		cliName := diagnostic.Identifier(m.CLI.Name)
		return runtime.NewError(
			runtime.CodeGeneral,
			runtime.ExitGeneral,
			"invalid saved snip",
			fmt.Sprintf("replace the snip with `%s snip set` or fix snips.yml", cliName),
			err,
		)
	}
	if _, err := validatePlaceholders(savedTokens); err != nil {
		cliName := diagnostic.Identifier(m.CLI.Name)
		return runtime.NewError(
			runtime.CodeGeneral,
			runtime.ExitGeneral,
			"invalid saved snip",
			fmt.Sprintf("replace the snip with `%s snip set` or fix snips.yml", cliName),
			err,
		)
	}
	if err := validateSnipTarget(cmd, root, m, savedTokens); err != nil {
		return err
	}

	expandedArgs, err := expandSnipArgs(cmd, savedTokens, leadingFlags, invokeArgs)
	if err != nil {
		return err
	}

	root.SetArgs(expandedArgs)
	execErr := root.ExecuteContext(cmd.Context())
	if execErr != nil {
		if f := errorOutputFormat(expandedArgs); f == "json" || f == "yaml" {
			_ = root.PersistentFlags().Set("output", f)
		}
	}
	return execErr
}

func validateSnipName(cmd *cobra.Command, name string) error {
	if len(name) == 0 || len(name) > maxSnipNameLen || !isValidSnipName(name) {
		return runtime.UsageError(cmd, runtime.WithUsageDetail(
			errors.New("invalid snip name"),
			"snip name must start with a letter or digit, contain only letters, digits, '.', '-', or '_', and be at most 64 characters",
		))
	}
	if reservedSnipNames[strings.ToLower(name)] {
		return runtime.UsageError(cmd, runtime.WithUsageDetail(
			errors.New("reserved snip name"),
			"snip name conflicts with a built-in snip subcommand (set, list, ls, delete, rm, run, help)",
		))
	}
	return nil
}

func isValidSnipName(s string) bool {
	for i, r := range s {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if i == 0 {
			if !isAlphaNum {
				return false
			}
			continue
		}
		if !isAlphaNum && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return len(s) > 0
}

func validateSnipTarget(errCmd, root *cobra.Command, m *config.Manifest, tokens []string) error {
	cliName := diagnostic.Identifier(m.CLI.Name)
	probe := make([]string, len(tokens))
	for i, tok := range tokens {
		probe[i] = replacePlaceholdersWithDummy(tok)
	}
	_, first, rest, helpOnly, err := splitLeadingRootFlags(root, probe)
	if err != nil {
		return runtime.UsageError(errCmd, runtime.WithUsageDetail(
			err,
			fmt.Sprintf("snip command has invalid global flags. Run `%s --help` to list flags", cliName),
		))
	}
	if helpOnly || first == "" {
		return runtime.UsageError(errCmd, runtime.WithUsageDetail(
			errors.New("missing target subcommand"),
			fmt.Sprintf("snip command must start with a runnable `%s` subcommand", cliName),
		))
	}
	if first == m.CLI.Name {
		return runtime.UsageError(errCmd, runtime.WithUsageDetail(
			errors.New("leading binary name in snip command"),
			fmt.Sprintf("omit the leading `%s` binary name and start with a subcommand", cliName),
		))
	}
	if first == snipCommandName || first == metaCommandName || first == "help" || first == "completion" {
		return runtime.UsageError(errCmd, runtime.WithUsageDetail(
			errors.New("unsupported snip target"),
			"snip cannot target `snip`, `help`, `completion`, or internal control commands",
		))
	}

	cur := findSubcommand(root, first)
	if cur == nil {
		return runtime.UsageError(errCmd, runtime.WithUsageDetail(
			errors.New("unknown target subcommand"),
			fmt.Sprintf("snip command must target an existing `%s` command. Run `%s --help` to list commands", cliName, cliName),
		))
	}
	remaining := rest
	for len(remaining) > 0 && cur.HasSubCommands() {
		_, nextName, nextRest, nextHelp, flagErr := splitLeadingCommandFlags(cur, remaining)
		if flagErr != nil || nextHelp || nextName == "" {
			break
		}
		child := findSubcommand(cur, nextName)
		if child == nil {
			break
		}
		cur = child
		remaining = nextRest
	}
	if cur.HasSubCommands() || !cur.Runnable() {
		return runtime.UsageError(errCmd, runtime.WithUsageDetail(
			errors.New("incomplete target subcommand"),
			fmt.Sprintf("snip command points to a command group instead of a runnable `%s` command", cliName),
		))
	}
	return nil
}

func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name || slices.Contains(child.Aliases, name) {
			return child
		}
	}
	return nil
}

func snipNotFoundError(cmd *cobra.Command) error {
	cliName := diagnostic.Identifier(cmd.Root().Name())
	return runtime.UsageError(cmd, runtime.WithUsageDetail(
		errors.New("snip not found"),
		fmt.Sprintf("no saved snip with that name. Run `%s snip list` to list saved snips", cliName),
	))
}

func rootOutputFormat(cmd *cobra.Command) string {
	format, _ := cmd.Root().PersistentFlags().GetString("output")
	if format == "" {
		return "table"
	}
	return format
}

func validateSnipOutputFormat(cmd *cobra.Command) error {
	format := rootOutputFormat(cmd)
	if !slices.Contains(runtime.FormatterNames(), format) {
		return runtime.UsageError(cmd, runtime.WithUsageDetail(
			errors.New("unsupported output format"),
			"--output accepts: "+strings.Join(runtime.FormatterNames(), ", "),
		))
	}
	return nil
}

func validateLeadingOutputFlag(cmd *cobra.Command, leadingFlags []string) error {
	for i := 0; i < len(leadingFlags); i++ {
		arg := leadingFlags[i]
		var val string
		hasVal := false
		switch {
		case arg == "--output" || arg == "-o":
			if i+1 < len(leadingFlags) {
				val = leadingFlags[i+1]
				hasVal = true
				i++
			}
		case strings.HasPrefix(arg, "--output="):
			val = strings.TrimPrefix(arg, "--output=")
			hasVal = true
		case strings.HasPrefix(arg, "-o="):
			val = strings.TrimPrefix(arg, "-o=")
			hasVal = true
		case strings.HasPrefix(arg, "-o") && len(arg) > 2:
			val = arg[2:]
			hasVal = true
		}
		if hasVal && !slices.Contains(runtime.FormatterNames(), val) {
			return runtime.UsageError(cmd, runtime.WithUsageDetail(
				errors.New("unsupported output format"),
				"--output accepts: "+strings.Join(runtime.FormatterNames(), ", "),
			))
		}
	}
	return nil
}

func writeSnipOutput(cmd *cobra.Command, value any, hints runtime.OutputHints) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return runtime.FormatOutput(data, rootOutputFormat(cmd), cmd.OutOrStdout(), hints)
}

func sanitizeOneLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsGraphic(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
