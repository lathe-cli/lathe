package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
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

// Build mounts a service command tree under root, driven entirely by specs.
// Replaces the previous per-operation function approach: every operation is
// data, the execution path is a single function.
func Build(root *cobra.Command, service string, specs []CommandSpec) error {
	if findChildCommand(root, service) != nil {
		return fmt.Errorf("module command %q conflicts with an existing root command", service)
	}
	svc := &cobra.Command{Use: service, Short: service + " API", GroupID: ModuleGroupID}
	for _, group := range buildGroups(service, specs) {
		svc.AddCommand(group)
	}
	if err := ValidateShortcuts(specs, rootCommandNames(root, svc)); err != nil {
		return err
	}
	root.AddCommand(svc)
	return mountShortcuts(root, specs)
}

func BuildFlat(root *cobra.Command, service string, specs []CommandSpec) error {
	groups := buildGroups(service, specs)
	seen := map[string]bool{}
	for _, group := range groups {
		group.GroupID = ModuleGroupID
		name := group.Name()
		if seen[name] {
			return fmt.Errorf("flat mount command %q conflicts with another generated command", name)
		}
		seen[name] = true
		if findChildCommand(root, name) != nil {
			return fmt.Errorf("flat mount command %q conflicts with existing root command", name)
		}
	}
	if err := ValidateShortcuts(specs, rootCommandNames(root, groups...)); err != nil {
		return err
	}
	root.AddCommand(groups...)
	return mountShortcuts(root, specs)
}

func buildGroups(service string, specs []CommandSpec) []*cobra.Command {
	groups := map[string]*cobra.Command{}
	ordered := make([]*cobra.Command, 0)
	for i := range specs {
		s := specs[i]
		g, ok := groups[s.Group]
		if !ok {
			g = &cobra.Command{Use: strings.ToLower(s.Group), Short: s.Group + " operations"}
			groups[s.Group] = g
			ordered = append(ordered, g)
		}
		c := buildCmd(s)
		AttachCatalogCommand(c, service, s)
		g.AddCommand(c)
	}
	return ordered
}

func buildCmd(s CommandSpec) *cobra.Command {
	vals := make(map[string]any, len(s.Params))
	var bodyFile string
	var bodySets []string
	var bodyStringSets []string
	var paginateAll bool
	var maxPages int
	var waitPoll bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:     s.Use,
		Aliases: s.Aliases,
		Short:   s.Short,
		Long:    s.Long,
		Example: s.Example,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := resolveSafeInputFlags(cmd, s.Params, vals); err != nil {
				return err
			}
			if err := validateRequiredSafeParams(cmd, s.Params, s.RequestBody != nil); err != nil {
				return err
			}

			var hostname string
			var clientOpts ClientOptions
			var err error
			refreshAuth := !dryRun
			if s.Security != nil && s.Security.Public {
				hostname, clientOpts, err = tryLoadHostOptionsMaybeRefresh(cmd, s.DefaultHostname, refreshAuth)
			} else {
				hostname, clientOpts, err = loadHostOptionsMaybeRefresh(cmd, s.DefaultHostname, refreshAuth)
			}
			if err != nil {
				return err
			}

			for _, p := range s.Params {
				if len(p.Enum) == 0 || !flagChangedOrDefault(cmd, p) {
					continue
				}
				raw := flagStringValue(vals[p.Name])
				valid := false
				for _, e := range p.Enum {
					if raw == e {
						valid = true
						break
					}
				}
				if !valid {
					return fmt.Errorf("invalid value %q for --%s: must be one of %s",
						raw, p.Flag, strings.Join(p.Enum, ", "))
				}
			}

			path := s.PathTpl
			q := url.Values{}
			hdrs := map[string]string{}
			form := url.Values{}
			vars := map[string]any{}
			for _, p := range s.Params {
				switch p.In {
				case InPath:
					v := vals[p.Name].(*string)
					path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(*v), 1)
					continue
				case InHeader:
					if !flagChangedOrDefault(cmd, p) {
						continue
					}
					hdrs[p.Name] = *vals[p.Name].(*string)
					continue
				case InVariable:
					if !flagChangedOrDefault(cmd, p) {
						continue
					}
					switch v := vals[p.Name].(type) {
					case *int64:
						vars[p.Name] = *v
					case *float64:
						vars[p.Name] = *v
					case *bool:
						vars[p.Name] = *v
					case *[]int64:
						vars[p.Name] = *v
					case *[]float64:
						vars[p.Name] = *v
					case *[]bool:
						vars[p.Name] = *v
					case *[]string:
						vars[p.Name] = *v
					case *string:
						vars[p.Name] = *v
					}
					continue
				case InFormData:
					if !flagChangedOrDefault(cmd, p) {
						continue
					}
					switch v := vals[p.Name].(type) {
					case *int64:
						form.Set(p.Name, strconv.FormatInt(*v, 10))
					case *bool:
						form.Set(p.Name, strconv.FormatBool(*v))
					case *string:
						form.Set(p.Name, *v)
					}
					continue
				}
				if !flagChangedOrDefault(cmd, p) {
					continue
				}
				switch v := vals[p.Name].(type) {
				case *int64:
					q.Set(p.Name, strconv.FormatInt(*v, 10))
				case *bool:
					q.Set(p.Name, strconv.FormatBool(*v))
				case *[]string:
					for _, vv := range *v {
						q.Add(p.Name, vv)
					}
				case *string:
					q.Set(p.Name, *v)
				}
			}
			if enc := q.Encode(); enc != "" {
				path = path + "?" + enc
			}

			var body any
			if len(form) > 0 {
				body = form
			} else if s.RequestBody != nil && s.RequestBody.Template != "" {
				hasFile := cmd.Flags().Changed("file")
				var fileData []byte
				if hasFile {
					fd, rerr := ReadBody(bodyFile)
					if rerr != nil {
						return rerr
					}
					fileData = fd
				}
				raw, berr := buildEnvelopeBody(s.RequestBody.Template, s.RequestBody.MergePath, vars, bodySets, bodyStringSets, fileData, hasFile)
				if berr != nil {
					return berr
				}
				body = raw
			} else if s.RequestBody != nil {
				switch {
				case cmd.Flags().Changed("set") || cmd.Flags().Changed("set-str"):
					if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
						return fmt.Errorf("request body media type %s requires --file; --set and --set-str only support JSON request bodies", s.RequestBody.MediaType)
					}
					raw, berr := buildBodyFromSet(bodySets, bodyStringSets)
					if berr != nil {
						return berr
					}
					body = raw
				case cmd.Flags().Changed("file"):
					raw, rerr := ReadBody(bodyFile)
					if rerr != nil {
						return rerr
					}
					body = raw
				case s.RequestBody.Required:
					if !supportsJSONBodyBuilder(s.RequestBody.MediaType) {
						return fmt.Errorf("request body media type %s requires --file", s.RequestBody.MediaType)
					}
					return fmt.Errorf("request body required: pass --file, --set, or --set-str")
				}
			}
			if err := validateRequiredVariableParams(s, body); err != nil {
				return err
			}
			if body != nil && s.RequestBody != nil && s.RequestBody.MediaType != "" {
				hdrs["Content-Type"] = s.RequestBody.MediaType
			}

			if v, err := cmd.Root().PersistentFlags().GetBool("debug"); err == nil && v {
				clientOpts.Debug = true
			}
			clientOpts.UserAgent = cmd.Root().Use
			clientOpts.Headers = hdrs
			if s.Output.ResponseMediaType != "" {
				clientOpts.Accept = s.Output.ResponseMediaType
			}
			if dryRun {
				return writeDryRun(cmd, s, hostname, path, body, clientOpts)
			}
			var data []byte
			if paginateAll && s.Output.Pagination != nil {
				data, err = PaginateAll(cmd.Context(), hostname, s.Method, path, body, clientOpts, *s.Output.Pagination, s.Output.ListPath, maxPages)
				if err != nil {
					return err
				}
			} else if waitPoll {
				r, rerr := DoRawFull(cmd.Context(), hostname, s.Method, path, body, clientOpts)
				if rerr != nil {
					return rerr
				}
				if r.StatusCode == 202 {
					if loc := r.Header.Get("Location"); loc != "" {
						data, err = PollUntilDone(cmd.Context(), hostname, loc, clientOpts, DefaultPollTimeout)
						if err != nil {
							return err
						}
					} else {
						data = r.Body
					}
				} else {
					data = r.Body
				}
			} else {
				data, err = DoRaw(cmd.Context(), hostname, s.Method, path, body, clientOpts)
				if err != nil {
					return err
				}
			}
			format, _ := cmd.Root().PersistentFlags().GetString("output")
			return FormatOutput(data, format, os.Stdout, s.Output)
		},
	}

	for i := range s.Params {
		p := s.Params[i]
		if p.In == InPath {
			v := new(string)
			vals[p.Name] = v
			cmd.Flags().StringVar(v, p.Flag, p.Default, p.Help)
			addSafeInputFlags(cmd, p)
			if p.Default == "" && !isSensitiveStringParam(p) {
				_ = cmd.MarkFlagRequired(p.Flag)
			}
			if p.Deprecated {
				_ = cmd.Flags().MarkDeprecated(p.Flag, "this flag is deprecated")
			}
			continue
		}
		switch p.GoType {
		case "int64":
			v := new(int64)
			vals[p.Name] = v
			var def int64
			if p.Default != "" {
				def, _ = strconv.ParseInt(p.Default, 10, 64)
			}
			cmd.Flags().Int64Var(v, p.Flag, def, p.Help)
		case "float64":
			v := new(float64)
			vals[p.Name] = v
			var def float64
			if p.Default != "" {
				def, _ = strconv.ParseFloat(p.Default, 64)
			}
			cmd.Flags().Float64Var(v, p.Flag, def, p.Help)
		case "bool":
			v := new(bool)
			vals[p.Name] = v
			def := p.Default == "true"
			cmd.Flags().BoolVar(v, p.Flag, def, p.Help)
		case "[]int64":
			v := new([]int64)
			vals[p.Name] = v
			cmd.Flags().Int64SliceVar(v, p.Flag, nil, p.Help)
		case "[]float64":
			v := new([]float64)
			vals[p.Name] = v
			cmd.Flags().Float64SliceVar(v, p.Flag, nil, p.Help)
		case "[]bool":
			v := new([]bool)
			vals[p.Name] = v
			cmd.Flags().BoolSliceVar(v, p.Flag, nil, p.Help)
		case "[]string":
			v := new([]string)
			vals[p.Name] = v
			cmd.Flags().StringSliceVar(v, p.Flag, nil, p.Help)
		default:
			v := new(string)
			vals[p.Name] = v
			cmd.Flags().StringVar(v, p.Flag, p.Default, p.Help)
			addSafeInputFlags(cmd, p)
		}
		if p.Required && p.Default == "" && (p.In != InVariable || s.RequestBody == nil) && !isSensitiveStringParam(p) {
			_ = cmd.MarkFlagRequired(p.Flag)
		}
		if p.Deprecated {
			_ = cmd.Flags().MarkDeprecated(p.Flag, "this flag is deprecated")
		}
	}
	if s.RequestBody != nil {
		fileHelp := "path to JSON body file, or '-' for stdin"
		setHelp := "set body field with type inference, e.g. --set spec.replicas=3 (repeatable; nested via dots)"
		setStrHelp := "set body field as string, e.g. --set-str spec.replicas=3 (repeatable; nested via dots)"
		if s.RequestBody.Required {
			suffix := " (use --file, --set, or --set-str)"
			fileHelp += suffix
			setHelp += suffix
			setStrHelp += suffix
		}
		cmd.Flags().StringVarP(&bodyFile, "file", "f", "", fileHelp)
		cmd.Flags().StringArrayVar(&bodySets, "set", nil, setHelp)
		cmd.Flags().StringArrayVar(&bodyStringSets, "set-str", nil, setStrHelp)
	}
	if s.Output.Pagination != nil {
		cmd.Flags().BoolVar(&paginateAll, "all", false, "fetch all pages")
		cmd.Flags().IntVar(&maxPages, "max-pages", DefaultMaxPages, "maximum pages to fetch with --all")
	}
	if s.Method == "POST" || s.Method == "PUT" || s.Method == "DELETE" || s.Method == "PATCH" {
		cmd.Flags().BoolVar(&waitPoll, "wait", false, "poll until long-running operation completes")
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print resolved request JSON without sending it")
	cmd.Hidden = s.Hidden
	if s.Deprecated {
		cmd.Deprecated = "this command is deprecated"
	}
	if s.Security != nil && len(s.Security.Scopes) > 0 {
		cmd.Long = fmt.Sprintf("%s\n\nRequired scopes: %s", cmd.Short, strings.Join(s.Security.Scopes, ", "))
	}
	return cmd
}

type dryRunRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
	Auth    dryRunAuth        `json:"auth"`
	Output  CatalogOutput     `json:"output"`
}

type dryRunAuth struct {
	Required bool     `json:"required"`
	Public   bool     `json:"public"`
	Scopes   []string `json:"scopes,omitempty"`
}

func writeDryRun(cmd *cobra.Command, s CommandSpec, hostname, path string, body any, opts ClientOptions) error {
	req, bodyBytes, _, err := resolveRequest(cmd.Context(), hostname, s.Method, path, body, opts)
	if err != nil {
		return err
	}
	out := dryRunRequest{
		Method:  req.Method,
		URL:     req.URL.String(),
		Headers: redactedDryRunHeaders(req.Header),
		Body:    redactedDryRunBody(req.Header.Get("Content-Type"), bodyBytes),
		Auth:    dryRunAuthForSpec(s),
		Output: CatalogOutput{
			ListPath:          s.Output.ListPath,
			DefaultColumns:    append([]string(nil), s.Output.DefaultColumns...),
			ResponseMediaType: s.Output.ResponseMediaType,
			Pagination:        catalogPagination(s.Output.Pagination),
			Streaming:         catalogStreaming(s.Output.Streaming),
		},
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func redactedDryRunHeaders(headers map[string][]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, vs := range headers {
		out[k] = redactDebugHeader(k, strings.Join(vs, ", "))
	}
	return out
}

func redactedDryRunBody(contentType string, body []byte) any {
	if len(body) == 0 {
		return nil
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

func dryRunAuthForSpec(s CommandSpec) dryRunAuth {
	out := dryRunAuth{Required: true}
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
		*vals[p.Name].(*string) = value
	}
	return nil
}

func validateRequiredSafeParams(cmd *cobra.Command, params []ParamSpec, hasRequestBody bool) error {
	for _, p := range params {
		if !p.Required || p.Default != "" || !isSensitiveStringParam(p) {
			continue
		}
		if p.In == InVariable && hasRequestBody {
			continue
		}
		if !flagChangedOrDefault(cmd, p) {
			return fmt.Errorf("required flag(s) \"%s\" not set", p.Flag)
		}
	}
	return nil
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
	var names []string
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
	if cmd.Flags().Changed(p.Flag) || p.Default != "" {
		return true
	}
	if !isSensitiveStringParam(p) {
		return false
	}
	return cmd.Flags().Changed(p.Flag+"-env") || cmd.Flags().Changed(p.Flag+"-file") || cmd.Flags().Changed(p.Flag+"-stdin")
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

func flagStringValue(v any) string {
	switch tv := v.(type) {
	case *string:
		return *tv
	case *int64:
		return strconv.FormatInt(*tv, 10)
	case *float64:
		return strconv.FormatFloat(*tv, 'f', -1, 64)
	case *bool:
		return strconv.FormatBool(*tv)
	case *[]int64:
		if len(*tv) > 0 {
			return strconv.FormatInt((*tv)[0], 10)
		}
		return ""
	case *[]float64:
		if len(*tv) > 0 {
			return strconv.FormatFloat((*tv)[0], 'f', -1, 64)
		}
		return ""
	case *[]bool:
		if len(*tv) > 0 {
			return strconv.FormatBool((*tv)[0])
		}
		return ""
	case *[]string:
		if len(*tv) > 0 {
			return (*tv)[0]
		}
		return ""
	}
	return ""
}
