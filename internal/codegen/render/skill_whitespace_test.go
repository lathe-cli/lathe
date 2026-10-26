package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/sourceconfig"
	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestRenderSkillDirectory_MarkdownWhitespace(t *testing.T) {
	for _, name := range []string{"plain", "no-modules", "optional-sections", "append", "replace"} {
		t.Run(name, func(t *testing.T) {
			chdirWithGeneratedRoot(t)
			manifest := &config.Manifest{CLI: config.CLIInfo{Name: "acmectl"}}
			modules := []SkillModule{
				{Source: &sourceconfig.Source{Name: "users"}, Specs: []runtime.CommandSpec{
					{Group: "Users", Use: "list", Method: "GET", Security: &runtime.SecurityHint{}},
				}},
				{Source: &sourceconfig.Source{Name: "hidden"}, Specs: []runtime.CommandSpec{
					{Group: "Internal", Use: "list", Method: "GET", Hidden: true},
				}},
			}
			if name == "no-modules" {
				modules = nil
			}
			if name == "optional-sections" {
				manifest.Auth.Login = &config.AuthLogin{Type: config.AuthLoginOAuthDevice}
				manifest.Contexts = map[string]config.ContextInfo{"organization": {Env: "ACME_ORG"}}
				manifest.Update.GitHub = &config.GitHubUpdate{Owner: "acme", Repo: "cli"}
			}
			include := SkillInclude{}
			if name == "append" || name == "replace" {
				include.Path = "include"
				body := "## Local guidance \t\r\n\n    Keep indentation.\t\n\n \t\n"
				for _, path := range []string{"SKILL.md", "references/catalog.md", "references/modules/users.md", "references/local.md", "assets/guide.md"} {
					file := filepath.Join(include.Path, filepath.FromSlash(path))
					testutil.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
					testutil.NoError(t, os.WriteFile(file, []byte(body), 0o644))
				}
				testutil.NoError(t, os.WriteFile("include/assets/data.txt", []byte("preserve \t\n\n"), 0o644))
				if name == "replace" {
					include.Files = map[string]SkillFileAction{
						"SKILL.md":                    SkillFileReplace,
						"references/catalog.md":       SkillFileReplace,
						"references/modules/users.md": SkillFileReplace,
					}
				}
			}
			root := filepath.Join("skills", "acmectl")
			testutil.NoError(t, RenderSkillDirectoryWithInclude(root, manifest, modules, include))
			testutil.NoError(t, RenderSkillBundlePackage(root, manifest.CLI.Name))
			count := 0
			testutil.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				rel, err := filepath.Rel(root, path)
				if err != nil || strings.HasPrefix(entry.Name(), ".") {
					return err
				}
				body := readFile(t, root, rel)
				bundled := readFile(t, filepath.Join(SkillBundleDir, "acmectl"), rel)
				testutil.Check(t, bundled == body, "%s differs from its bundled copy", rel)
				if filepath.Ext(path) != ".md" {
					return nil
				}
				count++
				testutil.Check(t, strings.HasSuffix(body, "\n") && !strings.HasSuffix(body, "\n\n"), "%s must end with one newline", rel)
				for line, text := range strings.Split(body, "\n") {
					testutil.Check(t, strings.TrimRight(text, " \t\r") == text, "%s:%d has trailing whitespace", rel, line+1)
				}
				return nil
			}))
			wantCount := 4
			if name == "no-modules" {
				wantCount = 2
			}
			if include.Path != "" {
				wantCount = 6
				body := readFile(t, root, "references/local.md")
				testutil.Check(t, body == "## Local guidance\n\n    Keep indentation.\n", "include content changed: %q", body)
				testutil.Check(t, readFile(t, root, "assets/data.txt") == "preserve \t\n\n", "non-Markdown content changed")
			}
			testutil.Check(t, count == wantCount, "Markdown count = %d, want %d", count, wantCount)
			for _, path := range []string{"SKILL.md", "references/catalog.md"} {
				body := readFile(t, root, path)
				testutil.Check(t, strings.Contains(body, "## Cloudflare Access\n") == (name != "replace"), "%s has unexpected Access guidance", path)
			}
			if len(modules) > 0 {
				testutil.Check(t, !strings.Contains(readFile(t, root, "references/modules/hidden.md"), "## Cloudflare Access\n"), "module reference contains Access guidance")
			}
		})
	}
}
