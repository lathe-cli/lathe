package specsync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func syncSwagger(src *sourceconfig.Source, workDir, syncDir string) error {
	for i, rel := range src.Swagger.Files {
		srcPath := filepath.Join(workDir, rel)
		dstPath := filepath.Join(syncDir, rel)
		if _, err := os.Stat(srcPath); err != nil {
			return fmt.Errorf("missing %s in %s@%s", rel, src.Name, src.PinnedTag)
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "   %s [%d/%d] -> %s\n", src.Name, i+1, len(src.Swagger.Files), rel)
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
