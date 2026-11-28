package specsync

import (
	"fmt"
	"os"
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
	current := rootAbs
	for _, part := range append([]string{"."}, strings.Split(inside, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("inspect source path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("unsafe path: symlink at %s", current)
		}
	}
	return full, nil
}
