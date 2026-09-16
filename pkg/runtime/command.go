package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func buildCmd(s CommandSpec) *cobra.Command {
	vals := make(map[string]any, len(s.Params))
	var bodyFile string
	var bodySets []string
	var bodyStringSets []string
	var bodyFileFlag string
	var paginateAll bool
	var maxPages int
	var waitPoll bool
	var dryRun bool
	var liveStream bool

	positionals := positionalParams(s.Params)
	cmd := &cobra.Command{
		Use:     s.Use,
		Aliases: s.Aliases,
		Short:   s.Short,
		Long:    s.Long,
		Example: s.Example,
		Args:    UsageArgs(cobra.NoArgs),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.ValidateRequiredFlags(); err != nil {
				return UsageError(cmd, WithUsageDetail(err, requiredFlagsDetail(cmd)))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			if _, ok := formatters[format]; !ok {
				return UsageError(cmd, WithUsageDetail(fmt.Errorf("unsupported output format"), outputFormatDetail()))
			}
			if liveStream && format != "table" {
				return UsageError(cmd, fmt.Errorf("live stream output does not support -o %s", format))
			}
			if liveStream && waitPoll {
				return UsageError(cmd, fmt.Errorf("live stream output does not support wait polling"))
			}
			changed := operationChangedFlags(cmd, s.Params)
			if err := bindPositionalArgs(cmd, args, positionals, changed); err != nil {
				return UsageError(cmd, err)
			}
			if err := resolveSafeInputFlags(cmd, s.Params, vals); err != nil {
				return inputError(cmd, err)
			}
			input := OperationInput{Values: vals, Changed: changed}
			if err := resolveCommandContexts(cmd, s, &input); err != nil {
				return err
			}
			if err := validateRequiredParams(s.Params, s.RequestBody != nil, changed); err != nil {
				return UsageError(cmd, err)
			}

			hasFile := bodyFileFlag != "" && cmd.Flags().Changed(bodyFileFlag)
			var fileBody []byte
			var err error
			if hasFile {
				fileBody, err = ReadBodyContext(cmd.Context(), bodyFile)
				if err != nil {
					return inputError(cmd, err)
				}
			}
			input.FileBody = fileBody
			input.HasFile = hasFile
			input.BodySets = bodySets
			input.BodyStringSets = bodyStringSets
			if err := validateOperationInput(s, input); err != nil {
				return UsageError(cmd, err)
			}

			host, clientOpts, err := operationHostOptions(cmd, s, dryRun)
			if err != nil {
				return err
			}
			if !dryRun {
				var reporter hostReporter
				reporter.noticeImplicitHost(cmd.ErrOrStderr(), host)
			}

			output := operationOutput{}
			if s.Output.Streaming != nil && format == "raw" && !waitPoll {
				output.raw = cmd.OutOrStdout()
			} else if liveStream && format == "table" {
				output.live = cmd.OutOrStdout()
			}
			result, err := invokeOperation(cmd.Context(), s, input, OperationOptions{
				Hostname:    host.Hostname,
				HostSource:  host.Source,
				Client:      clientOpts,
				DryRun:      dryRun,
				PaginateAll: paginateAll,
				MaxPages:    maxPages,
				Wait:        waitPoll,
			}, output)
			if err != nil {
				return apiErrorWithKnownDetail(s, err)
			}
			if result.DryRun != nil {
				return writeDryRun(*result.DryRun, cmd.OutOrStdout())
			}
			if output.raw != nil || output.live != nil {
				return nil
			}
			return FormatOutput(result.Data, format, cmd.OutOrStdout(), s.Output)
		},
	}
	if len(positionals) > 0 {
		for _, p := range positionals {
			cmd.Use += " [" + p.Argument + "]"
		}
		cmd.Args = UsageArgs(cobra.MaximumNArgs(len(positionals)))
	}
	configureParamFlagAliases(cmd, s.Params)

	for i := range s.Params {
		bindParamFlag(cmd, vals, s.Params[i], s.RequestBody != nil)
	}
	if s.RequestBody != nil && !hasFormDataParams(s.Params) {
		bodyFileFlag = controlFlagName(cmd, "file")
		bodySetFlag := controlFlagName(cmd, "set")
		bodyStringSetFlag := controlFlagName(cmd, "set-str")
		fileHelp := "path to JSON body file, or '-' for stdin"
		setHelp := fmt.Sprintf("set body field with type inference, e.g. --%s spec.replicas=3 (repeatable; nested via dots)", bodySetFlag)
		setStrHelp := fmt.Sprintf("set body field as string, e.g. --%s spec.replicas=3 (repeatable; nested via dots)", bodyStringSetFlag)
		if s.RequestBody.Required {
			suffix := fmt.Sprintf(" (use --%s, --%s, or --%s)", bodyFileFlag, bodySetFlag, bodyStringSetFlag)
			fileHelp += suffix
			setHelp += suffix
			setStrHelp += suffix
		}
		if len(s.RequestBody.SetOnlyFields) > 0 {
			suffix := fmt.Sprintf(" (no typed flags for body fields: %s)", strings.Join(s.RequestBody.SetOnlyFields, ", "))
			setHelp += suffix
			setStrHelp += suffix
		}
		cmd.Flags().StringVarP(&bodyFile, bodyFileFlag, "f", "", fileHelp)
		cmd.Flags().StringArrayVar(&bodySets, bodySetFlag, nil, setHelp)
		cmd.Flags().StringArrayVar(&bodyStringSets, bodyStringSetFlag, nil, setStrHelp)
	}
	if s.Output.Pagination != nil {
		allFlag := controlFlagName(cmd, "all")
		maxPagesFlag := controlFlagName(cmd, "max-pages")
		cmd.Flags().BoolVar(&paginateAll, allFlag, false, "fetch all pages")
		cmd.Flags().IntVar(&maxPages, maxPagesFlag, DefaultMaxPages, "maximum pages to fetch with --"+allFlag)
	}
	if s.Method == "POST" || s.Method == "PUT" || s.Method == "DELETE" || s.Method == "PATCH" {
		cmd.Flags().BoolVar(&waitPoll, controlFlagName(cmd, "wait"), false, "poll until long-running operation completes")
	}
	if s.Output.Streaming != nil && s.Output.Streaming.Policy != nil && s.Output.Streaming.Policy.Live != nil {
		cmd.Flags().BoolVar(&liveStream, controlFlagName(cmd, "stream"), false, "print configured stream fields as they arrive (requires -o table)")
	}
	dryRunFlag := controlFlagName(cmd, "dry-run")
	cmd.Flags().BoolVar(&dryRun, dryRunFlag, false, "print resolved request JSON without sending it")
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[catalogDryRunWiredAnnotation] = dryRunFlag
	cmd.Hidden = s.Hidden
	if s.Deprecated {
		cmd.Deprecated = "this command is deprecated"
	}
	if s.Security != nil && len(s.Security.Scopes) > 0 {
		cmd.Long = fmt.Sprintf("%s\n\nRequired scopes: %s", cmd.Short, strings.Join(s.Security.Scopes, ", "))
	}
	return cmd
}

func inputError(cmd *cobra.Command, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return UsageError(cmd, err)
}

func apiErrorWithKnownDetail(s CommandSpec, err error) error {
	var he *HTTPError
	if !errors.As(err, &he) {
		return err
	}
	le := ClassifyError(err)
	if le.Detail == "" {
		for _, ke := range s.KnownErrors {
			if ke.Status == he.Status && ke.Cause != "" {
				le.Detail = sanitizeErrorDetail(ke.Cause)
				break
			}
		}
	}
	return le
}

func requiredFlagsDetail(cmd *cobra.Command) string {
	missing := make([]string, 0)
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			return
		}
		if slices.Contains(f.Annotations[cobra.BashCompOneRequiredFlag], "true") {
			missing = append(missing, "--"+f.Name)
		}
	})
	if len(missing) == 0 {
		return ""
	}
	return "missing required: " + strings.Join(missing, ", ")
}

func outputFormatDetail() string {
	return "--output accepts: " + strings.Join(FormatterNames(), ", ")
}

func controlFlagName(cmd *cobra.Command, name string) string {
	if cmd.Flags().Lookup(name) == nil {
		return name
	}
	base := "lathe-" + name
	if cmd.Flags().Lookup(base) == nil {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if cmd.Flags().Lookup(candidate) == nil {
			return candidate
		}
	}
}
