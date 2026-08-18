package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const SchemaVersion = 12

type CommandSpec struct {
	Group           string
	GroupShort      string `json:",omitempty"`
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
	Notes           []string        `json:",omitempty"`
	Prerequisites   []string        `json:",omitempty"`
	KnownErrors     []KnownError    `json:",omitempty"`
	SetContext      *ContextSetHint `json:",omitempty"`
}

type ContextSetHint struct {
	Name  string `json:"name"`
	Param string `json:"param"`
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
	Aliases    []string `json:",omitempty"`
	Argument   string   `json:",omitempty"`
	In         string
	GoType     string
	Help       string
	Required   bool
	Default    string
	Enum       []string
	Format     string
	Deprecated bool
	Context    string `json:",omitempty"`
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
	Required          bool
	MediaType         string                 `json:",omitempty"`
	Schema            *SchemaSpec            `json:",omitempty"`
	SchemaDefinitions map[string]*SchemaSpec `json:"-"`
	RuntimeSchema     *RuntimeSchemaSpec     `json:",omitempty"`

	Template  string `json:",omitempty"`
	MergePath string `json:",omitempty"`
}

type RuntimeSchemaSpec struct {
	Operation    CommandSpec
	ResponsePath string
	Params       map[string]string
}

type SchemaSpec struct {
	Ref                        string                    `json:"ref,omitempty"`
	Type                       string                    `json:"type,omitempty"`
	Nullable                   bool                      `json:"nullable,omitempty"`
	AcceptStringEncodedInteger bool                      `json:"-"`
	AcceptStringEncodedNumber  bool                      `json:"-"`
	AcceptIntegerEnum          bool                      `json:"-"`
	AcceptIntegerID            bool                      `json:"-"`
	AcceptSingletonArray       bool                      `json:"-"`
	Properties                 map[string]*SchemaSpec    `json:"properties,omitempty"`
	Required                   []string                  `json:"required,omitempty"`
	Items                      *SchemaSpec               `json:"items,omitempty"`
	AnyOf                      []*SchemaSpec             `json:"anyOf,omitempty"`
	OneOf                      []*SchemaSpec             `json:"oneOf,omitempty"`
	AllOf                      []*SchemaSpec             `json:"allOf,omitempty"`
	AdditionalProperties       *AdditionalPropertiesSpec `json:"additionalProperties,omitempty"`
}

type AdditionalPropertiesSpec struct {
	Allowed bool
	Schema  *SchemaSpec
}

func (s AdditionalPropertiesSpec) MarshalJSON() ([]byte, error) {
	if s.Schema != nil {
		return json.Marshal(s.Schema)
	}
	return json.Marshal(s.Allowed)
}

func (s *AdditionalPropertiesSpec) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("true")) || bytes.Equal(data, []byte("false")) {
		s.Schema = nil
		return json.Unmarshal(data, &s.Allowed)
	}
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("additionalProperties must be a boolean or schema")
	}
	var schema SchemaSpec
	if err := json.Unmarshal(data, &schema); err != nil {
		return err
	}
	s.Allowed = false
	s.Schema = &schema
	return nil
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
	Policy   *StreamPolicy `json:"Policy,omitempty"`
}

type StreamPolicy struct {
	DataFormat    string             `json:"data_format"`
	EventNamePath string             `json:"event_name_path,omitempty"`
	Collect       *StreamCollectHint `json:"collect"`
	Live          *StreamLiveHint    `json:"live,omitempty"`
}

type StreamCollectHint struct {
	RequireStop bool              `json:"require_stop,omitempty"`
	StopEvents  []string          `json:"stop_events,omitempty"`
	PauseEvents []string          `json:"pause_events,omitempty"`
	ErrorEvents []string          `json:"error_events,omitempty"`
	Fields      []StreamFieldRule `json:"fields,omitempty"`
}

type StreamFieldRule struct {
	Events []string `json:"events"`
	From   string   `json:"from,omitempty"`
	Value  string   `json:"value,omitempty"`
	To     string   `json:"to"`
	Reduce string   `json:"reduce"`
}

type StreamLiveHint struct {
	Events []string `json:"events"`
	From   string   `json:"from"`
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
	When           []WorkflowCondition
	Params         map[string]string
	BodySets       []WorkflowValue
	BodyStringSets []WorkflowValue
}

// WorkflowCondition guards a workflow step. Conditions on one step are joined
// with AND; Values within one condition are joined with OR.
type WorkflowCondition struct {
	Value    string
	Operator string
	Values   []string
}

type WorkflowValue struct {
	Name  string
	Value string
}
