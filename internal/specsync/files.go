package specsync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func syncFiles(src *sourceconfig.Source, files []string, workDir, syncDir string) error {
	for i, rel := range files {
		srcPath, err := safeJoin(workDir, rel)
		if err != nil {
			return err
		}
		dstPath, err := safeJoin(syncDir, rel)
		if err != nil {
			return err
		}
		if _, err := os.Stat(srcPath); err != nil {
			return fmt.Errorf("missing %s in %s@%s", rel, src.Name, src.PinnedTag)
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
		progress := src.Name
		if src.Backend != sourceconfig.BackendGraphQL {
			progress += fmt.Sprintf(" [%d/%d]", i+1, len(files))
		}
		fmt.Fprintf(os.Stderr, "   %s -> %s\n", progress, rel)
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
