package specsync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func syncProto(src *sourceconfig.Source, workDir, syncDir string) error {
	for _, st := range src.Proto.Staging {
		from := filepath.Join(workDir, st.From)
		to := filepath.Join(syncDir, st.To)
		if _, err := os.Stat(from); err != nil {
			return fmt.Errorf("staging %s: source missing: %w", st.From, err)
		}
		if err := copyProtoTree(from, to); err != nil {
			return fmt.Errorf("staging %s -> %s: %w", st.From, st.To, err)
		}
		fmt.Fprintf(os.Stderr, "   %s stage %s -> %s\n", src.Name, st.From, st.To)
	}
	entries := make([]string, 0, len(src.Proto.Entries))
	for _, e := range src.Proto.Entries {
		full := filepath.Join(syncDir, e)
		if _, err := os.Stat(full); err != nil {
			return fmt.Errorf("entry %s not found in staged tree: %w", e, err)
		}
		entries = append(entries, e)
	}
	descOut := filepath.Join(syncDir, "descriptor_set.pb")
	args := []string{
		"-I", syncDir,
		"--include_imports",
		"--include_source_info",
		"--descriptor_set_out=" + descOut,
	}
	for _, r := range src.Proto.ImportRoots {
		args = append(args, "-I", filepath.Join(syncDir, r))
	}
	args = append(args, entries...)
	cmd := exec.Command("protoc", args...)
	cmd.Dir = syncDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("protoc: %w", err)
	}
	fmt.Fprintf(os.Stderr, "   %s descriptor_set.pb generated\n", src.Name)
	return nil
}

func copyProtoTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".proto") {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(to, rel))
	})
}
