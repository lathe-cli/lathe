package runtime

import "encoding/json"

const SchemaVersion = 9

type CommandSpec struct {
	Group           string
	Use             string
	Aliases         []string
	Shortcuts       []CommandShortcut `json:",omitempty"`
	Short           string
	Long            string
	Example         string
	Examples        []CommandExample `json:",omitempty"`
	OperationID     string
	Hidden          bool
	Deprecated      bool
	Method          string
	PathTpl         string
	DefaultHostname string `json:",omitempty"`
	Params          []ParamSpec
	RequestBody     *RequestBody
	Output          OutputHints
	Security        *SecurityHint
	Notes           []string     `json:",omitempty"`
	Prerequisites   []string     `json:",omitempty"`
	KnownErrors     []KnownError `json:",omitempty"`
}

type CommandExample struct {
	Summary          string              `json:"summary,omitempty"`
	Command          string              `json:"command,omitempty"`
	BodyShape        json.RawMessage     `json:"body_shape,omitempty"`
	OutputHints      *ExampleOutputHints `json:"output_hints,omitempty"`
	FollowUpCommands []string            `json:"follow_up_commands,omitempty"`
}

type ExampleOutputHints struct {
	IDPath   string `json:"id_path,omitempty"`
	ListPath string `json:"list_path,omitempty"`
}

type CommandShortcut struct {
	Use    string            `json:"use"`
	Params map[string]string `json:"params,omitempty"`
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
	InVariable = "variable"
	InInput    = "input"
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
	Required   []string               `json:"required,omitempty"`
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

type WorkflowSpec struct {
	Use        string
	Aliases    []string
	Short      string
	Long       string
	Example    string
	Hidden     bool
	Deprecated bool
	Params     []ParamSpec
	Steps      []WorkflowStepSpec
	OutputFrom string
	Output     OutputHints
}

type WorkflowStepSpec struct {
	ID             string
	Operation      CommandSpec
	Params         map[string]string
	BodySets       []WorkflowValue
	BodyStringSets []WorkflowValue
}

type WorkflowValue struct {
	Name  string
	Value string
}
