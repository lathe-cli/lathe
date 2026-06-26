package specsync

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func safeJoin(root, rel string) (string, error) {
	if err := sourceconfig.ValidateRelPath("source path", rel); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root path: %w", err)
	}
	full := filepath.Join(rootAbs, rel)
	inside, err := filepath.Rel(rootAbs, full)
	if err != nil {
		return "", fmt.Errorf("resolve source path %q: %w", rel, err)
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path source path: %q", rel)
	}
	return full, nil
}
