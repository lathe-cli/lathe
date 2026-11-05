package skillbundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitup "github.com/lathe-cli/kitup/go"
	kitupcobra "github.com/lathe-cli/kitup/go-cobra"
)

func TestStatusComparesBundleWithoutChangingInstalledFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed string
		owner     string
		scope     kitup.Scope
		wantHint  bool
		wantError bool
	}{
		{name: "outdated user", installed: "old", owner: "demo", scope: kitup.UserScope, wantHint: true},
		{name: "outdated project", installed: "old", owner: "demo", scope: kitup.ProjectScope, wantHint: true},
		{name: "current", installed: "current", owner: "demo", scope: kitup.UserScope},
		{name: "missing", scope: kitup.UserScope},
		{name: "other owner", installed: "old", owner: "other", scope: kitup.UserScope, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			base := kitup.BaseOptions{Home: home, CWD: cwd}
			bundle := func(version string) kitup.SkillBundle {
				return kitup.FilesBundle([]kitup.SkillFile{{Path: "SKILL.md", Contents: []byte("---\nname: demo\ndescription: Demo\n---\n" + version + "\n")}})
			}
			if tc.installed != "" {
				report, err := kitup.InstallBundledSkill(kitup.InstallOptions{
					BaseOptions: base, AppID: tc.owner, SkillBundle: bundle(tc.installed), Scope: tc.scope, Agents: kitup.ExplicitAgents("codex"),
				})
				if err != nil || len(report.Installed) != 1 {
					t.Fatalf("install fixture: report=%+v error=%v", report, err)
				}
			}
			statusOpts := kitup.StatusOptions{BaseOptions: base, AppID: "demo", SkillName: "demo", Scope: tc.scope, Agents: kitup.ExplicitAgents("codex")}
			want, err := kitup.StatusBundledSkill(statusOpts)
			if err != nil {
				t.Fatal(err)
			}
			root := home
			if tc.scope == kitup.ProjectScope {
				root = cwd
			}
			target := filepath.Join(root, ".agents", "skills", "demo")
			before := installedFiles(t, target)
			var out, stderr bytes.Buffer
			cmd := newSkillCommand(kitupcobra.Options{AppID: "demo", Bundle: bundle("current"), Home: home, CWD: cwd})
			cmd.SilenceErrors = true
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"status", "--scope", string(tc.scope), "--agent", "codex", "--json"})
			err = cmd.Execute()
			if (err != nil) != tc.wantError {
				t.Fatalf("status error=%v, wantError=%v", err, tc.wantError)
			}
			var got kitup.StatusReport
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("status JSON: %v: %s", err, out.String())
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("status contract changed: got=%+v want=%+v", got, want)
			}
			hasHint := strings.Contains(stderr.String(), "skill install --scope "+string(tc.scope)+" --agent codex --dry-run")
			if hasHint != tc.wantHint {
				t.Fatalf("refresh hint=%v, want=%v: %s", hasHint, tc.wantHint, stderr.String())
			}
			if after := installedFiles(t, target); !reflect.DeepEqual(before, after) {
				t.Fatalf("status changed installed files: before=%v after=%v", before, after)
			}
		})
	}
}

func installedFiles(t *testing.T, target string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range []string{"SKILL.md", ".kitup.json"} {
		body, err := os.ReadFile(filepath.Join(target, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(body)
	}
	return files
}
