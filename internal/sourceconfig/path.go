package sourceconfig

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func validateSourceID(name string) error {
	if err := ValidateRelPath("source ID", name); err != nil {
		return err
	}
	if name == "." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("unsafe path source ID: %q", name)
	}
	return nil
}

func validateRelPathList(field string, paths []string) error {
	for _, p := range paths {
		if err := ValidateRelPath(field, p); err != nil {
			return err
		}
	}
	return nil
}

func ValidateRelPath(field, value string) error {
	if value == "" {
		return fmt.Errorf("unsafe path %s: empty path", field)
	}
	if filepath.IsAbs(value) || !filepath.IsLocal(value) {
		return fmt.Errorf("unsafe path %s: %q", field, value)
	}
	for _, part := range strings.Split(strings.ReplaceAll(value, "\\", "/"), "/") {
		if part == "" || part == ".." {
			return fmt.Errorf("unsafe path %s: %q", field, value)
		}
	}
	return nil
}

func resolveLocalPath(baseDir, raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("local_path must not be empty")
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		if u.Scheme != "file" {
			return "", fmt.Errorf("local_path must be a filesystem path or file:// URL")
		}
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("local_path file:// URL must not include a remote host")
		}
		raw = filepath.FromSlash(u.Path)
		if raw == "" {
			return "", fmt.Errorf("local_path file:// URL must include a path")
		}
	} else if strings.Contains(raw, "://") {
		return "", fmt.Errorf("local_path must be a filesystem path or file:// URL")
	}
	if colon := strings.IndexByte(raw, ':'); colon > 0 && strings.Contains(raw[:colon], "@") && !strings.ContainsAny(raw[:colon], `/\`) {
		return "", fmt.Errorf("local_path must be a filesystem path or file:// URL")
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(baseDir, raw)
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve local_path: %w", err)
	}
	return abs, nil
}

func validateRef(ref string) error {
	if isFloating40Hex := len(ref) == 40 && allHex(ref); isFloating40Hex {
		return nil
	}
	switch ref {
	case "HEAD", "main", "master":
		return floatingRefError(ref)
	}
	if strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/remotes/") ||
		strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") || strings.ContainsAny(ref, " \t\r\n~^:?*[\\") {
		return floatingRefError(ref)
	}
	return nil
}

func floatingRefError(ref string) error {
	return fmt.Errorf("pinned_tag %q looks like a floating ref; only immutable tags or 40-char SHAs are accepted", ref)
}

func allHex(s string) bool {
	return strings.Trim(s, "0123456789abcdef") == ""
}

func validateStaging(field string, entries []StagingEntry) error {
	for _, entry := range entries {
		if err := ValidateRelPath(field+".from", entry.From); err != nil {
			return err
		}
		if err := ValidateRelPath(field+".to", entry.To); err != nil {
			return err
		}
	}
	return nil
}
