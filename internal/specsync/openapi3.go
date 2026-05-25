package specsync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func syncOpenAPI3(src *sourceconfig.Source, workDir, syncDir string) error {
	for i, rel := range src.OpenAPI3.Files {
		srcPath := filepath.Join(workDir, rel)
		dstPath := filepath.Join(syncDir, rel)
		if _, err := os.Stat(srcPath); err != nil {
			return fmt.Errorf("missing %s in %s@%s", rel, src.Name, src.PinnedTag)
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "   %s [%d/%d] -> %s\n", src.Name, i+1, len(src.OpenAPI3.Files), rel)
	}
	return nil
}
