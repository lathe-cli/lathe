package specsync

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
)

func syncProto(src *sourceconfig.Source, workDir, syncDir, workRoot string) error {
	for _, st := range src.Proto.Staging {
		from, err := safeJoin(workDir, st.From)
		if err != nil {
			return err
		}
		to, err := safeJoin(syncDir, st.To)
		if err != nil {
			return err
		}
		if _, err := os.Stat(from); err != nil {
			return fmt.Errorf("staging %s: source missing: %w", st.From, err)
		}
		if err := copyProtoTree(from, to); err != nil {
			return fmt.Errorf("staging %s -> %s: %w", st.From, st.To, err)
		}
		fmt.Fprintf(os.Stderr, "   %s stage %s -> %s\n", src.Name, st.From, st.To)
	}
	if err := stageProtoDependencies(src, syncDir, workRoot); err != nil {
		return err
	}
	entries := make([]string, 0, len(src.Proto.Entries))
	for _, e := range src.Proto.Entries {
		full, err := safeJoin(syncDir, e)
		if err != nil {
			return err
		}
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
		full, err := safeJoin(syncDir, r)
		if err != nil {
			return err
		}
		args = append(args, "-I", full)
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
		dst := filepath.Join(to, rel)
		if existing, err := os.ReadFile(dst); err == nil {
			incoming, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(existing, incoming) {
				return fmt.Errorf("proto staging collision at %s", dst)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		return copyFile(path, dst)
	})
}
