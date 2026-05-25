package lathe

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func commandsCmd(m *config.Manifest) *cobra.Command {
	var jsonOut bool
	var includeHidden bool
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List generated commands",
		RunE: func(cmd *cobra.Command, _ []string) error {
			catalog := runtime.BuildCatalog(cmd.Root(), catalogOptions(m, includeHidden))
			if jsonOut {
				return writeJSON(cmd, catalog)
			}
			for i, entry := range catalog.Commands {
				if i > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				writeCommandSummary(cmd, entry)
			}
			return nil
		},
	}
	addJSONFlag(cmd, &jsonOut, "Emit catalog JSON")
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "Include hidden commands")
	cmd.AddCommand(commandsShowCmd(m), commandsSchemaCmd())
	return cmd
}

func commandsShowCmd(m *config.Manifest) *cobra.Command {
	var jsonOut bool
	var includeHidden bool
	cmd := &cobra.Command{
		Use:   "show <path...>",
		Short: "Show one generated command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, ok := runtime.FindCatalogCommand(cmd.Root(), args, catalogOptions(m, includeHidden))
			if !ok {
				return fmt.Errorf("generated command not found: %s", strings.Join(args, " "))
			}
			if jsonOut {
				return writeJSON(cmd, entry)
			}
			writeCommandSummary(cmd, entry)
			if len(entry.Flags) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "Flags:")
				for _, flag := range entry.Flags {
					required := ""
					if flag.Required {
						required = " required"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  --%s %s%s  %s\n", flag.Flag, flag.Type, required, flag.Help)
				}
			}
			return nil
		},
	}
	addJSONFlag(cmd, &jsonOut, "Emit command JSON")
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "Include hidden commands")
	return cmd
}

func commandsSchemaCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print command catalog schema version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			data := map[string]int{"catalog_schema_version": runtime.CatalogSchemaVersion}
			if jsonOut {
				return writeJSON(cmd, data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "catalog schema %d\n", runtime.CatalogSchemaVersion)
			return nil
		},
	}
	addJSONFlag(cmd, &jsonOut, "Emit schema JSON")
	return cmd
}

func searchCmd(m *config.Manifest) *cobra.Command {
	var jsonOut bool
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search generated commands",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			results := runtime.SearchCatalog(cmd.Root(), query, runtime.SearchOptions{
				CatalogOptions: catalogOptions(m, false),
				Limit:          limit,
			})
			if jsonOut {
				return writeJSON(cmd, results)
			}
			for i, result := range results {
				if i > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				writeCommandSummary(cmd, result.Command)
			}
			return nil
		},
	}
	addJSONFlag(cmd, &jsonOut, "Emit search results JSON")
	cmd.Flags().IntVar(&limit, "limit", runtime.DefaultSearchLimit, "Maximum number of results")
	return cmd
}

func catalogOptions(m *config.Manifest, includeHidden bool) runtime.CatalogOptions {
	return runtime.CatalogOptions{
		CLIName:       m.CLI.Name,
		CLIVersion:    Version,
		IncludeHidden: includeHidden,
	}
}

func writeCommandSummary(cmd *cobra.Command, entry runtime.CatalogCommand) {
	fmt.Fprintln(cmd.OutOrStdout(), strings.Join(entry.Path, " "))
	if entry.Summary != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", entry.Summary)
	}
	if entry.HTTP.Method != "" || entry.HTTP.PathTemplate != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s %s\n", entry.HTTP.Method, entry.HTTP.PathTemplate)
	}
}

func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func addJSONFlag(cmd *cobra.Command, target *bool, usage string) {
	cmd.Flags().BoolVar(target, "json", false, usage)
}
