package rawir

type RawModule struct {
	Name       string
	Operations []RawOperation
	Schemas    map[string]*RawSchema
}

type RawOperation struct {
	Group       string
	OperationID string
	Summary     string
	Description string
	Method      string
	Path        string
	Parameters  []RawParameter
	RequestBody *RawRequestBody
	Responses   map[string]*RawResponse
	Produces    []string
	Security    []RawSecurityReq
	Output      *RawOutputHints `json:",omitempty"`
}

type RawSecurityReq struct {
	Scopes []string
}

type RawParameter struct {
	Name        string
	In          string
	Required    bool
	Type        string
	Description string
	Default     string
	Enum        []string
	Format      string
	Deprecated  bool
}

type RawRequestBody struct {
	Required  bool
	MediaType string     `json:",omitempty"`
	Schema    *RawSchema `json:",omitempty"`

	Template  string `json:",omitempty"`
	MergePath string `json:",omitempty"`
}

type RawResponse struct {
	Schema    *RawSchema
	MediaType string
}

type RawOutputHints struct {
	ListPath          string   `json:",omitempty"`
	DefaultColumns    []string `json:",omitempty"`
	ResponseMediaType string   `json:",omitempty"`
}

type RawSchema struct {
	Ref        string
	Type       string
	Properties map[string]*RawSchema
	Items      *RawSchema
}

const RefPrefix = "#/definitions/"

func Resolve(s *RawSchema, defs map[string]*RawSchema) *RawSchema {
	if s == nil {
		return nil
	}
	if s.Ref == "" {
		return s
	}
	if len(s.Ref) < len(RefPrefix) || s.Ref[:len(RefPrefix)] != RefPrefix {
		return nil
	}
	return defs[s.Ref[len(RefPrefix):]]
}
