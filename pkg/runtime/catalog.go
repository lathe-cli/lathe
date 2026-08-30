package runtime

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
)

const CatalogSchemaVersion = 21
const DefaultSearchLimit = 20

const (
	MutationRead    = "read"
	MutationWrite   = "write"
	MutationUnknown = "unknown"
)

const (
	DryRunHTTPPreview = "http_preview"
	DryRunUnsupported = "unsupported"
)

const (
	CatalogSurfaceCommands       = "commands"
	CatalogSurfaceCommandsShow   = "commands.show"
	CatalogSurfaceCommandsSchema = "commands.schema"
	CatalogSurfaceSearch         = "search"
)

const catalogCommandAnnotation = "lathe.catalog.command"
const catalogCapabilitiesAnnotation = "lathe.catalog.capabilities"
const catalogDryRunWiredAnnotation = "lathe.dry_run.wired"
const CapabilitySkillBundle = "skill.bundle"
const CapabilityWorkflowDSL = "workflow.dsl"

type CatalogOptions struct {
	CLIName       string
	CLIVersion    string
	IncludeHidden bool
	Capabilities  []string
}

type SearchOptions struct {
	CatalogOptions
	Limit int
}

type Catalog struct {
	CatalogSchemaVersion int                  `json:"catalog_schema_version"`
	CLI                  CatalogCLI           `json:"cli"`
	Output               CatalogOutputFormats `json:"output"`
	Commands             []CatalogCommand     `json:"commands"`
}

type CatalogCLI struct {
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type CatalogOutputFormats struct {
	DefaultFormat string   `json:"default_format"`
	Formats       []string `json:"formats"`
}

type CatalogCommand struct {
	Kind          string             `json:"kind"`
	Path          []string           `json:"path"`
	Service       string             `json:"service"`
	Group         string             `json:"group"`
	Use           string             `json:"use"`
	Aliases       []string           `json:"aliases,omitempty"`
	Shortcuts     []CommandShortcut  `json:"shortcuts,omitempty"`
	Summary       string             `json:"summary,omitempty"`
	Description   string             `json:"description,omitempty"`
	Example       string             `json:"example,omitempty"`
	Examples      []CommandExample   `json:"examples,omitempty"`
	OperationID   string             `json:"operation_id,omitempty"`
	HTTP          CatalogHTTP        `json:"http"`
	Workflow      *CatalogWorkflow   `json:"workflow,omitempty"`
	Auth          CatalogAuth        `json:"auth"`
	Mutation      string             `json:"mutation"`
	DryRun        *CatalogDryRun     `json:"dry_run"`
	Body          *CatalogBody       `json:"body,omitempty"`
	Flags         []CatalogFlag      `json:"flags"`
	Output        CatalogOutput      `json:"output"`
	Hidden        bool               `json:"hidden"`
	Deprecated    bool               `json:"deprecated"`
	Notes         []string           `json:"notes,omitempty"`
	Prerequisites []string           `json:"prerequisites,omitempty"`
	KnownErrors   []KnownError       `json:"known_errors,omitempty"`
	SetsContext   *CatalogContextSet `json:"sets_context,omitempty"`
}

type CatalogWorkflow struct {
	DSL        string                `json:"dsl"`
	OutputFrom string                `json:"output_from,omitempty"`
	Steps      []CatalogWorkflowStep `json:"steps"`
}

type CatalogWorkflowStep struct {
	ID          string                     `json:"id"`
	OperationID string                     `json:"operation_id,omitempty"`
	HTTP        CatalogHTTP                `json:"http"`
	When        []CatalogWorkflowCondition `json:"when,omitempty"`
	Contexts    []CatalogContextBinding    `json:"contexts,omitempty"`
	SetsContext *CatalogContextSet         `json:"sets_context,omitempty"`
}

type CatalogWorkflowCondition struct {
	Value    string   `json:"value"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type CatalogHTTP struct {
	Method          string `json:"method"`
	PathTemplate    string `json:"path_template"`
	DefaultHostname string `json:"default_hostname,omitempty"`
}

type CatalogAuth struct {
	Required bool     `json:"required"`
	Scopes   []string `json:"scopes,omitempty"`
}

type CatalogDryRun struct {
	Mode string `json:"mode"`
	Flag string `json:"flag,omitempty"`
}

type CatalogSchema struct {
	CatalogSchemaVersion int                 `json:"catalog_schema_version"`
	Surfaces             []string            `json:"surfaces"`
	DryRun               CatalogSchemaDryRun `json:"dry_run"`
}

type CatalogSchemaDryRun struct {
	Result string `json:"result"`
}

func CatalogSchemaDocument() CatalogSchema {
	return CatalogSchema{
		CatalogSchemaVersion: CatalogSchemaVersion,
		Surfaces: []string{
			CatalogSurfaceCommands,
			CatalogSurfaceCommandsShow,
			CatalogSurfaceCommandsSchema,
			CatalogSurfaceSearch,
		},
		DryRun: CatalogSchemaDryRun{Result: DryRunHTTPPreview},
	}
}

type CatalogBody struct {
	Required      bool                  `json:"required"`
	MediaType     string                `json:"media_type,omitempty"`
	Schema        *SchemaSpec           `json:"schema,omitempty"`
	RuntimeSchema *CatalogRuntimeSchema `json:"runtime_schema,omitempty"`
	Template      string                `json:"template,omitempty"`
	MergePath     string                `json:"merge_path,omitempty"`
}

type CatalogRuntimeSchema struct {
	OperationID  string                  `json:"operation_id"`
	HTTP         CatalogHTTP             `json:"http"`
	ResponsePath string                  `json:"response_path,omitempty"`
	Params       map[string]string       `json:"params,omitempty"`
	Contexts     []CatalogContextBinding `json:"contexts,omitempty"`
}

type CatalogFlag struct {
	Name       string                 `json:"name"`
	Flag       string                 `json:"flag"`
	Aliases    []string               `json:"aliases,omitempty"`
	Argument   string                 `json:"argument,omitempty"`
	Position   int                    `json:"position,omitempty"`
	Location   string                 `json:"location"`
	Type       string                 `json:"type"`
	Required   bool                   `json:"required"`
	Default    string                 `json:"default,omitempty"`
	Enum       []string               `json:"enum,omitempty"`
	ItemEnum   []string               `json:"item_enum,omitempty"`
	Format     string                 `json:"format,omitempty"`
	InputModes []string               `json:"input_modes,omitempty"`
	Deprecated bool                   `json:"deprecated"`
	Help       string                 `json:"help,omitempty"`
	Context    *CatalogContextBinding `json:"context,omitempty"`
}

type CatalogContextBinding struct {
	Name       string   `json:"name"`
	Env        string   `json:"env,omitempty"`
	Precedence []string `json:"precedence"`
}

type CatalogContextSet struct {
	Name      string `json:"name"`
	FromParam string `json:"from_param"`
}

type CatalogOutput struct {
	ListPath          string                  `json:"list_path,omitempty"`
	DefaultColumns    []string                `json:"default_columns,omitempty"`
	ColumnLabels      map[string]string       `json:"column_labels,omitempty"`
	ColumnFormats     map[string]ColumnFormat `json:"column_formats,omitempty"`
	ResponseMediaType string                  `json:"response_media_type,omitempty"`
	Pagination        *CatalogPagination      `json:"pagination,omitempty"`
	Streaming         *CatalogStreaming       `json:"streaming,omitempty"`
}

type CatalogPagination struct {
	Strategy   string `json:"strategy"`
	TokenParam string `json:"token_param,omitempty"`
	TokenField string `json:"token_field,omitempty"`
	LimitParam string `json:"limit_param,omitempty"`
}

type CatalogStreaming struct {
	Strategy string        `json:"strategy"`
	Policy   *StreamPolicy `json:"policy,omitempty"`
}

type SearchResult struct {
	Score   int            `json:"score"`
	Command CatalogCommand `json:"command"`
}

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

func catalogCommand(service string, spec CommandSpec, path []string) CatalogCommand {
	flags := catalogFlags(spec.Params)
	examples := spec.Examples
	if len(examples) == 0 && spec.Example != "" {
		examples = []CommandExample{{Command: spec.Example}}
	}
	cmd := CatalogCommand{
		Kind:        "operation",
		Path:        append([]string(nil), path...),
		Service:     service,
		Group:       spec.Group,
		Use:         spec.Use,
		Aliases:     append([]string(nil), spec.Aliases...),
		Shortcuts:   cloneShortcuts(spec.Shortcuts),
		Summary:     spec.Short,
		Description: spec.Long,
		Example:     spec.Example,
		Examples:    examples,
		OperationID: spec.OperationID,
		HTTP:        CatalogHTTP{Method: spec.Method, PathTemplate: spec.PathTpl, DefaultHostname: spec.DefaultHostname},
		Auth:        catalogAuth(spec.Security),
		Mutation:    catalogMutation(spec),
		DryRun:      &CatalogDryRun{Mode: DryRunUnsupported},
		Flags:       flags,
		Output: CatalogOutput{
			ListPath:          spec.Output.ListPath,
			DefaultColumns:    append([]string(nil), spec.Output.DefaultColumns...),
			ColumnLabels:      copyStringMap(spec.Output.ColumnLabels),
			ColumnFormats:     copyColumnFormats(spec.Output.ColumnFormats),
			ResponseMediaType: spec.Output.ResponseMediaType,
			Pagination:        catalogPagination(spec.Output.Pagination),
			Streaming:         catalogStreaming(spec.Output.Streaming),
		},
		Hidden:        spec.Hidden,
		Deprecated:    spec.Deprecated,
		Notes:         append([]string(nil), spec.Notes...),
		Prerequisites: append([]string(nil), spec.Prerequisites...),
		KnownErrors:   append([]KnownError(nil), spec.KnownErrors...),
	}
	if spec.SetContext != nil {
		cmd.SetsContext = &CatalogContextSet{Name: spec.SetContext.Name, FromParam: spec.SetContext.Param}
	}
	if spec.RequestBody != nil {
		cmd.Body = &CatalogBody{
			Required:  spec.RequestBody.Required,
			MediaType: spec.RequestBody.MediaType,
			Schema:    spec.RequestBody.Schema,
			Template:  spec.RequestBody.Template,
			MergePath: spec.RequestBody.MergePath,
		}
		if binding := spec.RequestBody.RuntimeSchema; binding != nil {
			cmd.Body.RuntimeSchema = &CatalogRuntimeSchema{
				OperationID: binding.Operation.OperationID,
				HTTP: CatalogHTTP{
					Method:          binding.Operation.Method,
					PathTemplate:    binding.Operation.PathTpl,
					DefaultHostname: binding.Operation.DefaultHostname,
				},
				ResponsePath: binding.ResponsePath,
				Params:       copyStringMap(binding.Params),
				Contexts:     catalogContextBindings(binding.Operation.Params),
			}
		}
	}
	return cmd
}

func catalogWorkflowConditions(conditions []WorkflowCondition) []CatalogWorkflowCondition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]CatalogWorkflowCondition, 0, len(conditions))
	for _, cond := range conditions {
		out = append(out, CatalogWorkflowCondition{
			Value:    cond.Value,
			Operator: cond.Operator,
			Values:   append([]string(nil), cond.Values...),
		})
	}
	return out
}

func catalogWorkflowCommand(spec WorkflowSpec, path []string) CatalogCommand {
	flags := catalogFlags(spec.Params)
	steps := make([]CatalogWorkflowStep, 0, len(spec.Steps))
	auth := CatalogAuth{Required: false}
	for _, step := range spec.Steps {
		catalogStep := CatalogWorkflowStep{
			ID:          step.ID,
			OperationID: step.Operation.OperationID,
			HTTP: CatalogHTTP{
				Method:          step.Operation.Method,
				PathTemplate:    step.Operation.PathTpl,
				DefaultHostname: step.Operation.DefaultHostname,
			},
			When:     catalogWorkflowConditions(step.When),
			Contexts: catalogContextBindings(step.Operation.Params),
		}
		if step.Operation.SetContext != nil {
			catalogStep.SetsContext = &CatalogContextSet{Name: step.Operation.SetContext.Name, FromParam: step.Operation.SetContext.Param}
		}
		steps = append(steps, catalogStep)
		stepAuth := catalogAuth(step.Operation.Security)
		if stepAuth.Required {
			auth.Required = true
		}
		auth.Scopes = append(auth.Scopes, stepAuth.Scopes...)
	}
	auth.Scopes = normalizeCapabilities(auth.Scopes)
	return CatalogCommand{
		Kind:        "workflow",
		Path:        append([]string(nil), path...),
		Service:     "workflow",
		Group:       "workflow",
		Use:         spec.Use,
		Aliases:     append([]string(nil), spec.Aliases...),
		Summary:     spec.Short,
		Description: spec.Long,
		Example:     spec.Example,
		HTTP:        CatalogHTTP{},
		Workflow: &CatalogWorkflow{
			DSL:        "lathe.workflow.v1",
			OutputFrom: spec.OutputFrom,
			Steps:      steps,
		},
		Auth:     auth,
		Mutation: catalogWorkflowMutation(spec),
		DryRun:   &CatalogDryRun{Mode: DryRunUnsupported},
		Flags:    flags,
		Output: CatalogOutput{
			ListPath:          spec.Output.ListPath,
			DefaultColumns:    append([]string(nil), spec.Output.DefaultColumns...),
			ColumnLabels:      copyStringMap(spec.Output.ColumnLabels),
			ColumnFormats:     copyColumnFormats(spec.Output.ColumnFormats),
			ResponseMediaType: spec.Output.ResponseMediaType,
			Pagination:        catalogPagination(spec.Output.Pagination),
			Streaming:         catalogStreaming(spec.Output.Streaming),
		},
		Hidden:     spec.Hidden,
		Deprecated: spec.Deprecated,
	}
}

func catalogFlags(params []ParamSpec) []CatalogFlag {
	flags := make([]CatalogFlag, 0, len(params))
	position := 0
	for _, p := range params {
		var inputModes []string
		if isSensitiveStringParam(p) {
			inputModes = []string{"flag", "env", "file", "stdin"}
		}
		argumentPosition := 0
		if p.Argument != "" {
			position++
			argumentPosition = position
		}
		flags = append(flags, CatalogFlag{
			Name:       p.Name,
			Flag:       p.Flag,
			Aliases:    append([]string(nil), p.Aliases...),
			Argument:   p.Argument,
			Position:   argumentPosition,
			Location:   p.In,
			Type:       p.GoType,
			Required:   p.Required,
			Default:    p.Default,
			Enum:       append([]string(nil), p.Enum...),
			ItemEnum:   append([]string(nil), p.ItemEnum...),
			Format:     p.Format,
			InputModes: inputModes,
			Deprecated: p.Deprecated,
			Help:       p.Help,
			Context:    catalogContextBinding(p),
		})
	}
	return flags
}

func catalogContextBinding(param ParamSpec) *CatalogContextBinding {
	if param.Context == "" {
		return nil
	}
	info := config.Active().Contexts[param.Context]
	precedence := []string{"flag"}
	if info.Env != "" {
		precedence = append(precedence, "env")
	}
	precedence = append(precedence, "stored")
	return &CatalogContextBinding{Name: param.Context, Env: info.Env, Precedence: precedence}
}

func catalogContextBindings(params []ParamSpec) []CatalogContextBinding {
	out := make([]CatalogContextBinding, 0)
	for _, param := range params {
		if binding := catalogContextBinding(param); binding != nil {
			out = append(out, *binding)
		}
	}
	return out
}

func cloneShortcuts(shortcuts []CommandShortcut) []CommandShortcut {
	out := make([]CommandShortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		out = append(out, CommandShortcut{Use: shortcut.Use, Params: copyStringMap(shortcut.Params)})
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyColumnFormats(in map[string]ColumnFormat) map[string]ColumnFormat {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ColumnFormat, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func catalogPagination(p *PaginationHint) *CatalogPagination {
	if p == nil {
		return nil
	}
	return &CatalogPagination{
		Strategy:   p.Strategy,
		TokenParam: p.TokenParam,
		TokenField: p.TokenField,
		LimitParam: p.LimitParam,
	}
}

func catalogStreaming(s *StreamingHint) *CatalogStreaming {
	if s == nil {
		return nil
	}
	return &CatalogStreaming{Strategy: s.Strategy, Policy: s.Policy}
}

func catalogAuth(security *SecurityHint) CatalogAuth {
	if security == nil {
		return CatalogAuth{Required: true}
	}
	return CatalogAuth{Required: !security.Public, Scopes: append([]string(nil), security.Scopes...)}
}
