package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ModuleGroupID is the cobra group ID every generated service command tree
// attaches to. Root command must AddGroup this ID for help output to
// segregate modules from core commands (e.g. auth) and from completion/help.
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

// completionRootName is cobra's shell completion command. Cobra adds it
// lazily at Execute time, so findChildCommand cannot observe it while
// mounting; a mounted command using that name would silently suppress it.
const completionRootName = "completion"

// Build mounts a service command tree under root, driven entirely by specs.
// Replaces the previous per-operation function approach: every operation is
// data, the execution path is a single function.
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
				return UsageError(cmd, err)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			if _, ok := formatters[format]; !ok {
				return UsageError(cmd, fmt.Errorf("unsupported output format"))
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
				return UsageError(cmd, err)
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
				fileBody, err = ReadBody(bodyFile)
				if err != nil {
					return UsageError(cmd, err)
				}
			}
			input.FileBody = fileBody
			input.HasFile = hasFile
			input.BodySets = bodySets
			input.BodyStringSets = bodyStringSets
			if err := validateOperationInput(s, input); err != nil {
				return UsageError(cmd, err)
			}

			var hostname string
			var clientOpts ClientOptions
			refreshAuth := !dryRun
			if dryRun || (s.Security != nil && s.Security.Public) {
				hostname, clientOpts, err = tryLoadHostOptionsMaybeRefresh(cmd, s.DefaultHostname, refreshAuth)
			} else {
				hostname, clientOpts, err = loadHostOptionsMaybeRefresh(cmd, s.DefaultHostname, refreshAuth)
			}
			if err != nil {
				return err
			}

			if v, err := cmd.Root().PersistentFlags().GetBool("debug"); err == nil && v {
				clientOpts.Debug = true
			}
			clientOpts.UserAgent = cmd.Root().Use

			output := operationOutput{}
			if s.Output.Streaming != nil && format == "raw" && !waitPoll {
				output.raw = cmd.OutOrStdout()
			} else if liveStream && format == "table" {
				output.live = cmd.OutOrStdout()
			}
			result, err := invokeOperation(cmd.Context(), s, input, OperationOptions{
				Hostname:    hostname,
				Client:      clientOpts,
				DryRun:      dryRun,
				PaginateAll: paginateAll,
				MaxPages:    maxPages,
				Wait:        waitPoll,
			}, output)
			if err != nil {
				return err
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

func bindParamFlag(cmd *cobra.Command, vals map[string]any, p ParamSpec, hasRequestBody bool) {
	key := boundParamKey(p)
	if p.In == InPath {
		v := new(string)
		vals[key] = v
		cmd.Flags().StringVar(v, p.Flag, p.Default, p.Help)
		addSafeInputFlags(cmd, p)
		if p.Default == "" && p.Argument == "" && p.Context == "" && !isSensitiveStringParam(p) {
			_ = cmd.MarkFlagRequired(p.Flag)
		}
		if p.Deprecated {
			_ = cmd.Flags().MarkDeprecated(p.Flag, "this flag is deprecated")
		}
		return
	}
	switch p.GoType {
	case "int64":
		v := new(int64)
		vals[key] = v
		var def int64
		if p.Default != "" {
			def, _ = strconv.ParseInt(p.Default, 10, 64)
		}
		cmd.Flags().Int64Var(v, p.Flag, def, p.Help)
	case "float64":
		v := new(float64)
		vals[key] = v
		var def float64
		if p.Default != "" {
			def, _ = strconv.ParseFloat(p.Default, 64)
		}
		cmd.Flags().Float64Var(v, p.Flag, def, p.Help)
	case "bool":
		v := new(bool)
		vals[key] = v
		def := p.Default == "true"
		cmd.Flags().BoolVar(v, p.Flag, def, p.Help)
	case "[]int64":
		v := new([]int64)
		vals[key] = v
		cmd.Flags().Int64SliceVar(v, p.Flag, nil, p.Help)
	case "[]float64":
		v := new([]float64)
		vals[key] = v
		cmd.Flags().Float64SliceVar(v, p.Flag, nil, p.Help)
	case "[]bool":
		v := new([]bool)
		vals[key] = v
		cmd.Flags().BoolSliceVar(v, p.Flag, nil, p.Help)
	case "[]string":
		v := new([]string)
		vals[key] = v
		cmd.Flags().StringSliceVar(v, p.Flag, nil, p.Help)
	default:
		v := new(string)
		vals[key] = v
		cmd.Flags().StringVar(v, p.Flag, p.Default, p.Help)
		addSafeInputFlags(cmd, p)
	}
	if p.Required && p.Default == "" && p.Argument == "" && p.Context == "" && (p.In != InVariable || !hasRequestBody) && !isSensitiveStringParam(p) {
		_ = cmd.MarkFlagRequired(p.Flag)
	}
	if p.Deprecated {
		_ = cmd.Flags().MarkDeprecated(p.Flag, "this flag is deprecated")
	}
}

func redactedDryRunHeaders(headers map[string][]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, vs := range headers {
		out[k] = redactDebugHeader(k, strings.Join(vs, ", "), nil)
	}
	return out
}

func redactedDryRunBody(contentType string, body []byte) any {
	if len(body) == 0 {
		return nil
	}
	if isMultipartMediaType(contentType) {
		return fmt.Sprintf("<multipart body omitted: %d bytes>", len(body))
	}
	redacted := redactDebugBody(contentType, body)
	if strings.HasPrefix(contentType, "application/json") {
		var v any
		if err := json.Unmarshal(redacted, &v); err == nil {
			return v
		}
	}
	return string(redacted)
}

func dryRunAuthForSpec(s CommandSpec) DryRunAuth {
	out := DryRunAuth{Required: true}
	if s.Security != nil {
		out.Required = !s.Security.Public
		out.Public = s.Security.Public
		out.Scopes = append([]string(nil), s.Security.Scopes...)
	}
	return out
}

func supportsJSONBodyBuilder(mediaType string) bool {
	mt, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mediaType)), ";")
	mt = strings.TrimSpace(mt)
	return mt == "" || mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func addSafeInputFlags(cmd *cobra.Command, p ParamSpec) {
	if !isSensitiveStringParam(p) {
		return
	}
	cmd.Flags().String(p.Flag+"-env", "", "read --"+p.Flag+" from an environment variable")
	cmd.Flags().String(p.Flag+"-file", "", "read --"+p.Flag+" from a file")
	cmd.Flags().Bool(p.Flag+"-stdin", false, "read --"+p.Flag+" from stdin")
}

func resolveSafeInputFlags(cmd *cobra.Command, params []ParamSpec, vals map[string]any) error {
	for _, p := range params {
		if !isSensitiveStringParam(p) {
			continue
		}
		changed := 0
		for _, flag := range []string{p.Flag, p.Flag + "-env", p.Flag + "-file", p.Flag + "-stdin"} {
			if cmd.Flags().Changed(flag) {
				changed++
			}
		}
		if changed == 0 {
			continue
		}
		if changed > 1 {
			return fmt.Errorf("use only one of --%s, --%s-env, --%s-file, or --%s-stdin", p.Flag, p.Flag, p.Flag, p.Flag)
		}
		var value string
		switch {
		case cmd.Flags().Changed(p.Flag):
			continue
		case cmd.Flags().Changed(p.Flag + "-env"):
			name, _ := cmd.Flags().GetString(p.Flag + "-env")
			value = os.Getenv(name)
			if value == "" {
				return fmt.Errorf("environment variable %s is empty", name)
			}
		case cmd.Flags().Changed(p.Flag + "-file"):
			path, _ := cmd.Flags().GetString(p.Flag + "-file")
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value = string(data)
		case cmd.Flags().Changed(p.Flag + "-stdin"):
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			value = string(data)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("--%s value is empty", p.Flag)
		}
		*vals[boundParamKey(p)].(*string) = value
	}
	return nil
}

func validateRequiredParams(params []ParamSpec, hasRequestBody bool, changed map[string]bool) error {
	for _, p := range params {
		if !p.Required || p.Default != "" {
			continue
		}
		if p.In == InVariable && hasRequestBody {
			continue
		}
		if !changed[boundParamKey(p)] {
			return fmt.Errorf("required flag(s) \"%s\" not set", p.Flag)
		}
	}
	return nil
}

func positionalParams(params []ParamSpec) []ParamSpec {
	out := make([]ParamSpec, 0)
	for _, p := range params {
		if p.Argument != "" {
			out = append(out, p)
		}
	}
	return out
}

func configureParamFlagAliases(cmd *cobra.Command, params []ParamSpec) {
	aliases := map[string]string{}
	for _, p := range params {
		for _, alias := range p.Aliases {
			if alias == "" || alias == p.Flag {
				continue
			}
			aliases[alias] = p.Flag
			if isSensitiveStringParam(p) {
				for _, suffix := range []string{"-env", "-file", "-stdin"} {
					aliases[alias+suffix] = p.Flag + suffix
				}
			}
		}
	}
	if len(aliases) == 0 {
		return
	}
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if normalized := aliases[name]; normalized != "" {
			return pflag.NormalizedName(normalized)
		}
		return pflag.NormalizedName(name)
	})
}

func bindPositionalArgs(cmd *cobra.Command, args []string, params []ParamSpec, changed map[string]bool) error {
	for i, value := range args {
		p := params[i]
		if flagChanged(cmd, p) {
			return fmt.Errorf("parameter %q cannot use both argument %d and --%s", p.Name, i+1, p.Flag)
		}
		if err := cmd.Flags().Set(p.Flag, value); err != nil {
			return fmt.Errorf("parse argument %d for parameter %q: %w", i+1, p.Name, err)
		}
		key := boundParamKey(p)
		changed[key] = true
	}
	return nil
}

func flagChanged(cmd *cobra.Command, p ParamSpec) bool {
	if cmd.Flags().Changed(p.Flag) {
		return true
	}
	if !isSensitiveStringParam(p) {
		return false
	}
	return cmd.Flags().Changed(p.Flag+"-env") || cmd.Flags().Changed(p.Flag+"-file") || cmd.Flags().Changed(p.Flag+"-stdin")
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

func validateRequiredVariableParams(s CommandSpec, body any) error {
	if s.RequestBody == nil {
		return nil
	}
	required := make([]ParamSpec, 0)
	for _, p := range s.Params {
		if p.In == InVariable && p.Required && p.Default == "" {
			required = append(required, p)
		}
	}
	if len(required) == 0 {
		return nil
	}
	raw, ok := body.([]byte)
	if !ok || len(raw) == 0 {
		return fmt.Errorf("required body field missing: %s", required[0].Name)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("validate request body: %w", err)
	}
	for _, p := range required {
		v, ok := getNestedPath(doc, joinBodyPath(s.RequestBody.MergePath, p.Name))
		if !ok || v == nil {
			return fmt.Errorf("required body field missing: %s", p.Name)
		}
	}
	return nil
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
	case p.In != InVariable && p.GoType == "float64":
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

func flagChangedOrDefault(cmd *cobra.Command, p ParamSpec) bool {
	return flagChanged(cmd, p) || (p.Default != "" && p.Context == "")
}

func operationChangedFlags(cmd *cobra.Command, params []ParamSpec) map[string]bool {
	changed := make(map[string]bool, len(params))
	for _, p := range params {
		if flagChangedOrDefault(cmd, p) {
			changed[boundParamKey(p)] = true
		}
	}
	return changed
}

func isSensitiveStringParam(p ParamSpec) bool {
	if p.GoType != "string" {
		return false
	}
	if strings.EqualFold(p.Format, "password") {
		return true
	}
	name := sensitiveNameKey(p.Name + " " + p.Flag)
	for _, marker := range []string{"password", "secret", "credential", "apikey", "privatekey", "accesstoken", "refreshtoken", "bearertoken", "authtoken"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return sensitiveNameKey(p.Name) == "token" || sensitiveNameKey(p.Flag) == "token"
}

func sensitiveNameKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
