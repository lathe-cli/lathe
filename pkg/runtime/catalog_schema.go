package runtime

const CatalogSchemaVersion = 24

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
	SearchTerms   []string           `json:"search_terms,omitempty"`
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
	SetOnlyFields []string              `json:"set_only_fields,omitempty"`
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
	ColumnAlignments  map[string]string       `json:"column_alignments,omitempty"`
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
