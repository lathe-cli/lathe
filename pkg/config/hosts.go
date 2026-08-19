package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// https is the default and is stripped; http is an explicit downgrade and
// is preserved so downstream BaseURL honors it.
func NormalizeHostname(s string) string {
	h := strings.TrimRight(strings.TrimSpace(s), "/")
	if strings.HasPrefix(h, "http://") {
		return h
	}
	return strings.TrimPrefix(h, "https://")
}

// HostEntry mirrors gh's per-host record in hosts.yml.
type HostEntry struct {
	AuthType          string            `yaml:"auth_type,omitempty"`
	LoginType         string            `yaml:"login_type,omitempty"`
	LoginProvider     string            `yaml:"login_provider,omitempty"`
	User              string            `yaml:"user,omitempty"`
	OAuthToken        string            `yaml:"oauth_token,omitempty"`
	OAuthRefreshToken string            `yaml:"oauth_refresh_token,omitempty"`
	OAuthExpiresAt    int64             `yaml:"oauth_expires_at,omitempty"`
	APIKey            string            `yaml:"api_key,omitempty"`
	APIKeyHeader      string            `yaml:"api_key_header,omitempty"`
	BasicUser         string            `yaml:"basic_user,omitempty"`
	BasicPassword     string            `yaml:"basic_password,omitempty"`
	Insecure          bool              `yaml:"insecure,omitempty"`
	Selected          bool              `yaml:"selected,omitempty"`
	Contexts          map[string]string `yaml:"contexts,omitempty"`
}

type Hosts struct {
	entries map[string]HostEntry
	path    string
}

func configDir() (string, error) {
	m := Active().CLI
	if v := os.Getenv(m.ConfigDirEnv); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, m.ConfigDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", m.ConfigDir), nil
}

func hostsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hosts.yml"), nil
}

func LoadHosts() (*Hosts, error) {
	p, err := hostsPath()
	if err != nil {
		return nil, err
	}
	return loadHostsPath(p)
}

func loadHostsPath(p string) (*Hosts, error) {
	h := &Hosts{entries: map[string]HostEntry{}, path: p}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return h, nil
		}
		return nil, err
	}
	raw := map[string]HostEntry{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	// One-time rekey: migrate any legacy keys that include scheme/slashes.
	for k, v := range raw {
		norm := NormalizeHostname(k)
		if _, exists := h.entries[norm]; !exists || norm == k {
			h.entries[norm] = v
		}
	}
	return h, nil
}

func (h *Hosts) Set(hostname string, e HostEntry) {
	h.entries[NormalizeHostname(hostname)] = e
}

func (h *Hosts) Get(hostname string) (HostEntry, bool) {
	e, ok := h.entries[NormalizeHostname(hostname)]
	return e, ok
}

func (h *Hosts) Delete(hostname string) bool {
	k := NormalizeHostname(hostname)
	if _, ok := h.entries[k]; !ok {
		return false
	}
	delete(h.entries, k)
	return true
}

func (h *Hosts) Names() []string {
	out := make([]string, 0, len(h.entries))
	for k := range h.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (h *Hosts) Selected() string {
	for _, name := range h.Names() {
		if h.entries[name].Selected {
			return name
		}
	}
	return ""
}

func (h *Hosts) Select(hostname string) {
	k := NormalizeHostname(hostname)
	for name, e := range h.entries {
		e.Selected = name == k
		h.entries[name] = e
	}
}

func (h *Hosts) Save() error {
	return h.saveAtomic()
}

func MutateHosts(ctx context.Context, mutate func(*Hosts) error) error {
	p, err := hostsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fileLock := flock.New(p+".lock", flock.SetPermissions(0o600))
	locked, err := fileLock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return fmt.Errorf("lock %s: %w", p, err)
	}
	if !locked {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("lock %s: unavailable", p)
	}
	defer func() { _ = fileLock.Unlock() }()

	h, err := loadHostsPath(p)
	if err != nil {
		return err
	}
	if err := mutate(h); err != nil {
		return err
	}
	return h.saveAtomic()
}

func (h *Hosts) saveAtomic() error {
	data, err := yaml.Marshal(h.entries)
	if err != nil {
		return err
	}
	return writeFileAtomic(h.path, data)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpPath, path)
}
