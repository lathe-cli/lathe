package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
)

func TestGeneratedSkillBodyDiagnostics(t *testing.T) {
	dir := t.TempDir()
	manifest := &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}
	if err := RenderSkillDirectory(filepath.Join(dir, "skills", "acmectl"), manifest, nil); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"SKILL.md", "references/catalog.md"} {
		body := readFile(t, dir, "skills/acmectl/"+file)
		for _, want := range []string{"error.detail", "usage", "api_error", "untrusted data", "key.sub=value", "key[0]=value", "key=[]", "{...}", "[...]", "plain strings", "--file", "--limit 5", "acmectl search \"<intent>\"", "acmectl commands show <path...> --json"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing guidance %q", file, want)
			}
		}
	}
}
