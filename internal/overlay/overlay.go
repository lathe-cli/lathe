package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Override struct {
	Use           string                   `yaml:"use"`
	Match         OperationMatch           `yaml:"match"`
	Aliases       []string                 `yaml:"aliases"`
	Shortcuts     []Shortcut               `yaml:"shortcuts"`
	Short         string                   `yaml:"short"`
	Long          string                   `yaml:"long"`
	Example       string                   `yaml:"example"`
	Examples      []Example                `yaml:"examples"`
	Group         string                   `yaml:"group"`
	Hidden        *bool                    `yaml:"hidden"`
	Ignore        bool                     `yaml:"ignore"`
	Params        map[string]ParamOverride `yaml:"params"`
	Notes         []string                 `yaml:"notes"`
	Prerequisites []string                 `yaml:"prerequisites"`
	KnownErrors   []KnownError             `yaml:"known_errors"`
	Body          *BodyOverride            `yaml:"body"`
	Output        *OutputOverride          `yaml:"output"`
	Context       *ContextOverride         `yaml:"context"`
}

type ContextOverride struct {
	SetOnSuccess *ContextSetOnSuccess `yaml:"set_on_success"`
}

type ContextSetOnSuccess struct {
	Name      string `yaml:"name"`
	FromParam string `yaml:"from_param"`
}

type BodyOverride struct {
	Flags         bool                   `yaml:"flags"`
	RuntimeSchema *RuntimeSchemaOverride `yaml:"runtime_schema"`
}

type RuntimeSchemaOverride struct {
	OperationID  string            `yaml:"operation_id"`
	ResponsePath string            `yaml:"response_path"`
	Params       map[string]string `yaml:"params"`
}

type OutputOverride struct {
	DefaultColumns []string                        `yaml:"default_columns"`
	ColumnLabels   map[string]string               `yaml:"column_labels"`
	ColumnFormats  map[string]ColumnFormatOverride `yaml:"column_formats"`
	Streaming      *StreamingOverride              `yaml:"streaming"`
}

type ColumnFormatOverride struct {
	Kind              string `yaml:"kind"`
	Currency          string `yaml:"currency"`
	SourceScale       int    `yaml:"source_scale"`
	Grouping          bool   `yaml:"grouping"`
	MinFractionDigits int    `yaml:"min_fraction_digits"`
	MaxFractionDigits int    `yaml:"max_fraction_digits"`
}

type StreamingOverride struct {
	Data          string         `yaml:"data"`
	EventNamePath string         `yaml:"event_name_path"`
	Collect       *StreamCollect `yaml:"collect"`
	Live          *StreamLive    `yaml:"live"`
}

type StreamCollect struct {
	RequireStop bool              `yaml:"require_stop"`
	StopEvents  []string          `yaml:"stop_events"`
	PauseEvents []string          `yaml:"pause_events"`
	ErrorEvents []string          `yaml:"error_events"`
	Fields      []StreamFieldRule `yaml:"fields"`
}

type StreamFieldRule struct {
	Events []string `yaml:"events"`
	From   string   `yaml:"from"`
	Value  string   `yaml:"value"`
	To     string   `yaml:"to"`
	Reduce string   `yaml:"reduce"`
}

type StreamLive struct {
	Events []string `yaml:"events"`
	From   string   `yaml:"from"`
}

type OperationMatch struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

type Example struct {
	Summary          string             `yaml:"summary"`
	Command          string             `yaml:"command"`
	BodyShape        map[string]any     `yaml:"body_shape"`
	OutputHints      ExampleOutputHints `yaml:"output_hints"`
	FollowUpCommands []string           `yaml:"follow_up_commands"`
}

type ExampleOutputHints struct {
	IDPath   string `yaml:"id_path"`
	ListPath string `yaml:"list_path"`
}

type Shortcut struct {
	Use    string            `yaml:"use"`
	Params map[string]string `yaml:"params"`
}

type ParamOverride struct {
	Flag            string `yaml:"flag"`
	Argument        string `yaml:"argument"`
	Help            string `yaml:"help"`
	Required        bool   `yaml:"required"`
	Default         string `yaml:"default"`
	DeprecatedAlias bool   `yaml:"hidden"`
	Deprecated      bool   `yaml:"deprecated"`
	Context         string `yaml:"context"`
}

type KnownError struct {
	Status int    `yaml:"status"`
	Cause  string `yaml:"cause"`
}

type GroupOverride struct {
	Short string `yaml:"short"`
}

type Module struct {
	Defaults Defaults                 `yaml:"defaults"`
	Groups   map[string]GroupOverride `yaml:"groups"`
	Commands map[string]Override      `yaml:"commands"`
}

type Defaults struct {
	Pagination *PaginationDefaults `yaml:"pagination"`
}

type PaginationDefaults struct {
	MatchCommands []string          `yaml:"match_commands"`
	Params        map[string]string `yaml:"params"`
}

// LoadDir reads every <module>.yaml under dir and returns module overlays keyed
// by module name. An empty or non-existent dir yields an empty map without
// error; overlays are always optional.
func LoadDir(dir string) (map[string]Module, error) {
	out := map[string]Module{}
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("overlay: read dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, fmt.Errorf("overlay: read %s: %w", path, rerr)
		}
		var mod Module
		if yerr := yaml.Unmarshal(data, &mod); yerr != nil {
			return nil, fmt.Errorf("overlay: parse %s: %w", path, yerr)
		}
		module := strings.TrimSuffix(e.Name(), ".yaml")
		out[module] = mod
	}
	return out, nil
}
