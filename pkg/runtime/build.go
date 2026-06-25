package runtime

import (
	"fmt"
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
func Build(root *cobra.Command, service string, specs []CommandSpec) {
	svc := &cobra.Command{Use: service, Short: service + " API", GroupID: ModuleGroupID}
	for _, group := range buildGroups(service, specs) {
		svc.AddCommand(group)
	}
	if err := ValidateShortcuts(specs, rootCommandNames(root, svc)); err != nil {
		panic(err)
	}
	root.AddCommand(svc)
	if err := mountShortcuts(root, specs); err != nil {
		panic(err)
	}
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

	cmd := &cobra.Command{
		Use:     s.Use,
		Aliases: s.Aliases,
		Short:   s.Short,
		Long:    s.Long,
		Example: s.Example,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var hostname string
			var clientOpts ClientOptions
			var err error
			if s.Security != nil && s.Security.Public {
				hostname, clientOpts, err = tryLoadHostOptions(cmd, s.DefaultHostname)
			} else {
				hostname, clientOpts, err = loadHostOptions(cmd, s.DefaultHostname)
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
					return fmt.Errorf("request body required: pass --file, --set, or --set-str")
				}
			}

			if v, err := cmd.Root().PersistentFlags().GetBool("debug"); err == nil && v {
				clientOpts.Debug = true
			}
			clientOpts.UserAgent = cmd.Root().Use
			clientOpts.Headers = hdrs
			if s.Output.ResponseMediaType != "" {
				clientOpts.Accept = s.Output.ResponseMediaType
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
			if p.Default == "" {
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
		}
		if p.Required && p.Default == "" {
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
	cmd.Hidden = s.Hidden
	if s.Deprecated {
		cmd.Deprecated = "this command is deprecated"
	}
	if s.Security != nil && len(s.Security.Scopes) > 0 {
		cmd.Long = fmt.Sprintf("%s\n\nRequired scopes: %s", cmd.Short, strings.Join(s.Security.Scopes, ", "))
	}
	return cmd
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
	return cmd.Flags().Changed(p.Flag) || p.Default != ""
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
