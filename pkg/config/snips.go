package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

type SnipEntry struct {
	Command     string `yaml:"command"`
	Description string `yaml:"description,omitempty"`
}

type snipEntryYAML struct{ SnipEntry }

func (e *snipEntryYAML) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!null" {
			return fmt.Errorf("snip command must not be empty")
		}
		var cmd string
		if err := value.Decode(&cmd); err != nil {
			return err
		}
		e.Command = strings.TrimSpace(cmd)
		e.Description = ""
		return nil
	case yaml.MappingNode:
		var cmd, desc string
		seen := map[string]bool{}
		for i := 0; i+1 < len(value.Content); i += 2 {
			keyNode := value.Content[i]
			valNode := value.Content[i+1]
			var key string
			if err := keyNode.Decode(&key); err != nil {
				return err
			}
			if seen[key] {
				return fmt.Errorf("duplicate snip field %q", key)
			}
			seen[key] = true
			switch key {
			case "command":
				if err := valNode.Decode(&cmd); err != nil {
					return err
				}
			case "description":
				if err := valNode.Decode(&desc); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown snip field %q", key)
			}
		}
		e.Command = strings.TrimSpace(cmd)
		e.Description = strings.TrimSpace(desc)
		return nil
	default:
		return fmt.Errorf("snip entry must be a string or mapping")
	}
}

type Snips struct {
	entries  map[string]SnipEntry
	path     string
	lockHeld *atomic.Bool
}

func snipsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snips.yml"), nil
}

func LoadSnips() (*Snips, error) {
	p, err := snipsPath()
	if err != nil {
		return nil, err
	}
	return loadSnipsPath(p)
}

func loadSnipsPath(p string) (*Snips, error) {
	s := &Snips{entries: map[string]SnipEntry{}, path: p}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if len(root.Content) == 0 || root.Content[0].Tag == "!!null" {
		return s, nil
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: snips root must be a mapping", p)
	}
	raw := map[string]snipEntryYAML{}
	if err := root.Content[0].Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	for k, v := range raw {
		name := strings.TrimSpace(k)
		if name == "" {
			return nil, fmt.Errorf("parse %s: snip name must not be empty", p)
		}
		if v.Command == "" {
			return nil, fmt.Errorf("parse %s: snip %q command must not be empty", p, name)
		}
		if _, exists := s.entries[name]; exists {
			return nil, fmt.Errorf("parse %s: duplicate snip %q", p, name)
		}
		s.entries[name] = v.SnipEntry
	}
	return s, nil
}

func (s *Snips) Set(name string, e SnipEntry) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(e.Command) == "" {
		return fmt.Errorf("snip name and command must not be empty")
	}
	if s.entries == nil {
		s.entries = map[string]SnipEntry{}
	}
	s.entries[name] = SnipEntry{
		Command:     strings.TrimSpace(e.Command),
		Description: strings.TrimSpace(e.Description),
	}
	return nil
}

func (s *Snips) Get(name string) (SnipEntry, bool) {
	e, ok := s.entries[strings.TrimSpace(name)]
	return e, ok
}

func (s *Snips) Delete(name string) bool {
	k := strings.TrimSpace(name)
	if _, ok := s.entries[k]; !ok {
		return false
	}
	delete(s.entries, k)
	return true
}

func (s *Snips) Names() []string {
	out := make([]string, 0, len(s.entries))
	for k := range s.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Snips) Save() error {
	if s.path == "" || s.lockHeld != nil && s.lockHeld.Load() {
		return s.saveAtomic()
	}
	return withFileLock(context.Background(), s.path, s.saveAtomic)
}

func MutateSnips(ctx context.Context, mutate func(*Snips) error) error {
	p, err := snipsPath()
	if err != nil {
		return err
	}
	return withFileLock(ctx, p, func() error {
		s, err := loadSnipsPath(p)
		if err != nil {
			return err
		}
		s.lockHeld = new(atomic.Bool)
		s.lockHeld.Store(true)
		defer s.lockHeld.Store(false)
		if err := mutate(s); err != nil {
			return err
		}
		return s.saveAtomic()
	})
}

func (s *Snips) saveAtomic() error {
	for name, e := range s.entries {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(e.Command) == "" {
			return fmt.Errorf("snip name and command must not be empty")
		}
	}
	data, err := yaml.Marshal(s.entries)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}
