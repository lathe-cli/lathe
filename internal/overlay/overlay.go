package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Override struct {
	Aliases       []string                 `yaml:"aliases"`
	Shortcuts     []Shortcut               `yaml:"shortcuts"`
	Short         string                   `yaml:"short"`
	Long          string                   `yaml:"long"`
	Example       string                   `yaml:"example"`
	Group         string                   `yaml:"group"`
	Hidden        *bool                    `yaml:"hidden"`
	Ignore        bool                     `yaml:"ignore"`
	Params        map[string]ParamOverride `yaml:"params"`
	Notes         []string                 `yaml:"notes"`
	Prerequisites []string                 `yaml:"prerequisites"`
	KnownErrors   []KnownError             `yaml:"known_errors"`
}

type Shortcut struct {
	Use    string            `yaml:"use"`
	Params map[string]string `yaml:"params"`
}

type ParamOverride struct {
	Flag            string `yaml:"flag"`
	Help            string `yaml:"help"`
	Required        bool   `yaml:"required"`
	Default         string `yaml:"default"`
	DeprecatedAlias bool   `yaml:"hidden"`
	Deprecated      bool   `yaml:"deprecated"`
}

type KnownError struct {
	Status int    `yaml:"status"`
	Cause  string `yaml:"cause"`
}

type Module struct {
	Defaults Defaults            `yaml:"defaults"`
	Commands map[string]Override `yaml:"commands"`
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
