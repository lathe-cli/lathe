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
	root.AddCommand(svc)
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
	root.AddCommand(groups...)
	return nil
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
				hostname, clientOpts, err = TryLoadHostOptions(cmd)
			} else {
				hostname, clientOpts, err = LoadHostOptions(cmd)
			}
			if err != nil {
				return err
			}

			for _, p := range s.Params {
				if len(p.Enum) == 0 || !cmd.Flags().Changed(p.Flag) {
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
			for _, p := range s.Params {
				switch p.In {
				case InPath:
					v := vals[p.Name].(*string)
					path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(*v), 1)
					continue
				case InHeader:
					if !cmd.Flags().Changed(p.Flag) {
						continue
					}
					hdrs[p.Name] = *vals[p.Name].(*string)
					continue
				case InFormData:
					if !cmd.Flags().Changed(p.Flag) {
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
				if !cmd.Flags().Changed(p.Flag) && p.Default == "" {
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
				raw, berr := buildEnvelopeBody(s.RequestBody.Template, s.RequestBody.MergePath, bodySets, bodyStringSets, fileData, hasFile)
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
			_ = cmd.MarkFlagRequired(p.Flag)
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
		case "bool":
			v := new(bool)
			vals[p.Name] = v
			def := p.Default == "true"
			cmd.Flags().BoolVar(v, p.Flag, def, p.Help)
		case "[]string":
			v := new([]string)
			vals[p.Name] = v
			cmd.Flags().StringSliceVar(v, p.Flag, nil, p.Help)
		default:
			v := new(string)
			vals[p.Name] = v
			cmd.Flags().StringVar(v, p.Flag, p.Default, p.Help)
		}
		if p.Required {
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

func flagStringValue(v any) string {
	switch tv := v.(type) {
	case *string:
		return *tv
	case *int64:
		return strconv.FormatInt(*tv, 10)
	case *bool:
		return strconv.FormatBool(*tv)
	case *[]string:
		if len(*tv) > 0 {
			return (*tv)[0]
		}
		return ""
	}
	return ""
}
