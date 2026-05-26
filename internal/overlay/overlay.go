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

type ParamOverride struct {
	Flag       string `yaml:"flag"`
	Help       string `yaml:"help"`
	Default    string `yaml:"default"`
	Hidden     bool   `yaml:"hidden"`
	Deprecated bool   `yaml:"deprecated"`
}

type KnownError struct {
	Status int    `yaml:"status"`
	Cause  string `yaml:"cause"`
}

type moduleFile struct {
	Commands map[string]Override `yaml:"commands"`
}

// LoadDir reads every <module>.yaml under dir and returns a nested map keyed
// by module name then command Use string. An empty or non-existent dir yields
// an empty map without error — overlays are always optional.
func LoadDir(dir string) (map[string]map[string]Override, error) {
	out := map[string]map[string]Override{}
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
		var mf moduleFile
		if yerr := yaml.Unmarshal(data, &mf); yerr != nil {
			return nil, fmt.Errorf("overlay: parse %s: %w", path, yerr)
		}
		module := strings.TrimSuffix(e.Name(), ".yaml")
		out[module] = mf.Commands
	}
	return out, nil
}
