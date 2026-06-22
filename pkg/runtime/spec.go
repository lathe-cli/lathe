package runtime

const SchemaVersion = 6

type CommandSpec struct {
	Group         string
	Use           string
	Aliases       []string
	Short         string
	Long          string
	Example       string
	OperationID   string
	Hidden        bool
	Deprecated    bool
	Method        string
	PathTpl       string
	Params        []ParamSpec
	RequestBody   *RequestBody
	Output        OutputHints
	Security      *SecurityHint
	Notes         []string     `json:",omitempty"`
	Prerequisites []string     `json:",omitempty"`
	KnownErrors   []KnownError `json:",omitempty"`
}

type ParamSpec struct {
	Name       string
	Flag       string
	In         string
	GoType     string
	Help       string
	Required   bool
	Default    string
	Enum       []string
	Format     string
	Deprecated bool
}

const (
	InPath     = "path"
	InQuery    = "query"
	InHeader   = "header"
	InFormData = "formData"
)

type RequestBody struct {
	Required  bool
	MediaType string      `json:",omitempty"`
	Schema    *SchemaSpec `json:",omitempty"`

	Template  string `json:",omitempty"`
	MergePath string `json:",omitempty"`
}

type SchemaSpec struct {
	Ref        string                 `json:"ref,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Properties map[string]*SchemaSpec `json:"properties,omitempty"`
	Items      *SchemaSpec            `json:"items,omitempty"`
}

type OutputHints struct {
	ListPath          string
	DefaultColumns    []string
	ResponseMediaType string
	Pagination        *PaginationHint
	Streaming         *StreamingHint
}

type PaginationHint struct {
	Strategy   string
	TokenParam string
	TokenField string
	LimitParam string
}

type StreamingHint struct {
	Strategy string
}

type SecurityHint struct {
	Public bool
	Scopes []string
}

type KnownError struct {
	Status int    `json:"status,omitempty"`
	Cause  string `json:"cause,omitempty"`
}
