package runtime

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const CatalogSchemaVersion = 5
const DefaultSearchLimit = 20

const catalogCommandAnnotation = "lathe.catalog.command"

type CatalogOptions struct {
	CLIName       string
	CLIVersion    string
	IncludeHidden bool
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
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type CatalogOutputFormats struct {
	DefaultFormat string   `json:"default_format"`
	Formats       []string `json:"formats"`
}

type CatalogCommand struct {
	Path          []string      `json:"path"`
	Service       string        `json:"service"`
	Group         string        `json:"group"`
	Use           string        `json:"use"`
	Aliases       []string      `json:"aliases,omitempty"`
	Summary       string        `json:"summary,omitempty"`
	Description   string        `json:"description,omitempty"`
	Example       string        `json:"example,omitempty"`
	OperationID   string        `json:"operation_id,omitempty"`
	HTTP          CatalogHTTP   `json:"http"`
	Auth          CatalogAuth   `json:"auth"`
	Body          *CatalogBody  `json:"body,omitempty"`
	Flags         []CatalogFlag `json:"flags"`
	Output        CatalogOutput `json:"output"`
	Hidden        bool          `json:"hidden"`
	Deprecated    bool          `json:"deprecated"`
	Notes         []string      `json:"notes,omitempty"`
	Prerequisites []string      `json:"prerequisites,omitempty"`
	KnownErrors   []KnownError  `json:"known_errors,omitempty"`
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

type CatalogBody struct {
	Required  bool        `json:"required"`
	MediaType string      `json:"media_type,omitempty"`
	Schema    *SchemaSpec `json:"schema,omitempty"`
	Template  string      `json:"template,omitempty"`
	MergePath string      `json:"merge_path,omitempty"`
}

type CatalogFlag struct {
	Name       string   `json:"name"`
	Flag       string   `json:"flag"`
	Location   string   `json:"location"`
	Type       string   `json:"type"`
	Required   bool     `json:"required"`
	Default    string   `json:"default,omitempty"`
	Enum       []string `json:"enum,omitempty"`
	Format     string   `json:"format,omitempty"`
	Deprecated bool     `json:"deprecated"`
	Help       string   `json:"help,omitempty"`
}

type CatalogOutput struct {
	ListPath          string             `json:"list_path,omitempty"`
	DefaultColumns    []string           `json:"default_columns,omitempty"`
	ResponseMediaType string             `json:"response_media_type,omitempty"`
	Pagination        *CatalogPagination `json:"pagination,omitempty"`
	Streaming         *CatalogStreaming  `json:"streaming,omitempty"`
}

type CatalogPagination struct {
	Strategy   string `json:"strategy"`
	TokenParam string `json:"token_param,omitempty"`
	TokenField string `json:"token_field,omitempty"`
	LimitParam string `json:"limit_param,omitempty"`
}

type CatalogStreaming struct {
	Strategy string `json:"strategy"`
}

type SearchResult struct {
	Score   int            `json:"score"`
	Command CatalogCommand `json:"command"`
}

func AttachCatalogCommand(cmd *cobra.Command, service string, spec CommandSpec) {
	entry := catalogCommand(service, spec, nil)
	raw, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[catalogCommandAnnotation] = string(raw)
}

func BuildCatalog(root *cobra.Command, opts CatalogOptions) Catalog {
	if opts.CLIName == "" {
		opts.CLIName = root.Use
	}
	commands := make([]CatalogCommand, 0)
	walkCatalog(root, nil, opts, &commands)
	sort.Slice(commands, func(i, j int) bool {
		return slices.Compare(commands[i].Path, commands[j].Path) < 0
	})
	return Catalog{
		CatalogSchemaVersion: CatalogSchemaVersion,
		CLI:                  CatalogCLI{Name: opts.CLIName, Version: opts.CLIVersion},
		Output:               CatalogOutputFormats{DefaultFormat: "table", Formats: FormatterNames()},
		Commands:             commands,
	}
}

func FindCatalogCommand(root *cobra.Command, path []string, opts CatalogOptions) (CatalogCommand, bool) {
	cur := root
	canonical := make([]string, 0, len(path))
	for _, segment := range path {
		child := findChildCommand(cur, segment)
		if child == nil {
			return CatalogCommand{}, false
		}
		canonical = append(canonical, child.Name())
		cur = child
	}
	cmd, ok := catalogCommandFromAnnotation(cur, canonical)
	if !ok || (!opts.IncludeHidden && cmd.Hidden) {
		return CatalogCommand{}, false
	}
	return cmd, true
}

func SearchCatalog(root *cobra.Command, query string, opts SearchOptions) []SearchResult {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	tokens := searchTokens(query)
	if len(tokens) == 0 {
		return []SearchResult{}
	}
	fullQuery := strings.ToLower(query)
	normalizedQuery := normalizeSearchText(query)
	results := make([]SearchResult, 0)
	for _, cmd := range BuildCatalog(root, opts.CatalogOptions).Commands {
		view := newSearchView(cmd)
		score, ok := scoreCatalogCommand(view, tokens, fullQuery, normalizedQuery)
		if !ok {
			continue
		}
		results = append(results, SearchResult{Score: score, Command: cmd})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return slices.Compare(results[i].Command.Path, results[j].Command.Path) < 0
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
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
	flags := make([]CatalogFlag, 0, len(spec.Params))
	for _, p := range spec.Params {
		flags = append(flags, CatalogFlag{
			Name:       p.Name,
			Flag:       p.Flag,
			Location:   p.In,
			Type:       p.GoType,
			Required:   p.Required,
			Default:    p.Default,
			Enum:       append([]string(nil), p.Enum...),
			Format:     p.Format,
			Deprecated: p.Deprecated,
			Help:       p.Help,
		})
	}
	cmd := CatalogCommand{
		Path:        append([]string(nil), path...),
		Service:     service,
		Group:       spec.Group,
		Use:         spec.Use,
		Aliases:     append([]string(nil), spec.Aliases...),
		Summary:     spec.Short,
		Description: spec.Long,
		Example:     spec.Example,
		OperationID: spec.OperationID,
		HTTP:        CatalogHTTP{Method: spec.Method, PathTemplate: spec.PathTpl, DefaultHostname: spec.DefaultHostname},
		Auth:        catalogAuth(spec.Security),
		Flags:       flags,
		Output: CatalogOutput{
			ListPath:          spec.Output.ListPath,
			DefaultColumns:    append([]string(nil), spec.Output.DefaultColumns...),
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
	if spec.RequestBody != nil {
		cmd.Body = &CatalogBody{
			Required:  spec.RequestBody.Required,
			MediaType: spec.RequestBody.MediaType,
			Schema:    spec.RequestBody.Schema,
			Template:  spec.RequestBody.Template,
			MergePath: spec.RequestBody.MergePath,
		}
	}
	return cmd
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
	return &CatalogStreaming{Strategy: s.Strategy}
}

func catalogAuth(security *SecurityHint) CatalogAuth {
	if security == nil {
		return CatalogAuth{Required: true}
	}
	return CatalogAuth{Required: !security.Public, Scopes: append([]string(nil), security.Scopes...)}
}

type searchView struct {
	path         []string
	fullPath     string
	service      string
	group        string
	use          string
	aliases      []string
	summary      string
	description  string
	operationID  string
	method       string
	pathTemplate string
	flags        []searchFlagView
}

type searchFlagView struct {
	name string
	flag string
	help string
}

func newSearchView(cmd CatalogCommand) searchView {
	aliases := make([]string, 0, len(cmd.Aliases))
	for _, alias := range cmd.Aliases {
		aliases = append(aliases, strings.ToLower(alias))
	}
	flags := make([]searchFlagView, 0, len(cmd.Flags))
	for _, flag := range cmd.Flags {
		flags = append(flags, searchFlagView{
			name: strings.ToLower(flag.Name),
			flag: strings.ToLower(flag.Flag),
			help: strings.ToLower(flag.Help),
		})
	}
	path := make([]string, 0, len(cmd.Path))
	for _, segment := range cmd.Path {
		path = append(path, strings.ToLower(segment))
	}
	return searchView{
		path:         path,
		fullPath:     strings.Join(path, " "),
		service:      strings.ToLower(cmd.Service),
		group:        strings.ToLower(cmd.Group),
		use:          strings.ToLower(cmd.Use),
		aliases:      aliases,
		summary:      strings.ToLower(cmd.Summary),
		description:  strings.ToLower(cmd.Description),
		operationID:  strings.ToLower(cmd.OperationID),
		method:       strings.ToLower(cmd.HTTP.Method),
		pathTemplate: strings.ToLower(cmd.HTTP.PathTemplate),
		flags:        flags,
	}
}

func scoreCatalogCommand(cmd searchView, tokens []string, fullQuery string, normalizedQuery string) (int, bool) {
	score := 0
	matches := 0
	strong := false
	for _, token := range tokens {
		tokenScore := scoreToken(cmd, token)
		if tokenScore == 0 {
			continue
		}
		matches++
		if tokenScore >= 45 {
			strong = true
		}
		score += tokenScore
	}
	if matches == 0 || (!strong && matches < 2) {
		return 0, false
	}
	if fullQuery == cmd.fullPath || fullQuery == cmd.operationID || fullQuery == cmd.use ||
		normalizedQuery == normalizeSearchText(cmd.fullPath) ||
		normalizedQuery == normalizeSearchText(cmd.operationID) ||
		normalizedQuery == normalizeSearchText(cmd.use) {
		score += 100
	}
	return score, true
}

func scoreToken(cmd searchView, token string) int {
	score := 0
	score = max(score, scoreField(cmd.operationID, token, 90))
	score = max(score, scoreField(cmd.use, token, 80))
	for _, alias := range cmd.aliases {
		score = max(score, scoreField(alias, token, 80))
	}
	for _, segment := range cmd.path {
		if strings.HasPrefix(segment, token) {
			score = max(score, 60)
		}
	}
	score = max(score, scoreField(cmd.pathTemplate, token, 45))
	score = max(score, scoreField(cmd.summary, token, 30))
	score = max(score, scoreField(cmd.description, token, 30))
	score = max(score, scoreField(cmd.service, token, 30))
	score = max(score, scoreField(cmd.group, token, 30))
	score = max(score, scoreField(cmd.method, token, 30))
	for _, flag := range cmd.flags {
		score = max(score, scoreField(flag.flag, token, 25))
		score = max(score, scoreField(flag.name, token, 25))
		score = max(score, scoreField(flag.help, token, 10))
	}
	return score
}

func scoreField(field string, token string, value int) int {
	if strings.Contains(field, token) || strings.Contains(normalizeSearchText(field), token) {
		return value
	}
	return 0
}

func searchTokens(query string) []string {
	return strings.Fields(normalizeSearchText(query))
}

func normalizeSearchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if b.Len() > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
			prev = r
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		prev = 0
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
