package specsync

import (
	"fmt"
	"os"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func syncGraphQL(src *sourceconfig.Source, workDir, syncDir string) error {
	rel := src.GraphQL.Schema
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
	fmt.Fprintf(os.Stderr, "   %s -> %s\n", src.Name, rel)
	return nil
}
