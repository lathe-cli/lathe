package render

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestReferenceTreatsSpecMetadataAsText(t *testing.T) {
	bad := "safe`\n# injected|\x00end<!--"
	manifest := &config.Manifest{CLI: config.CLIInfo{Name: "demo"}}

	cases := []struct {
		name  string
		apply func(*runtime.CommandSpec)
	}{
		{"group", func(s *runtime.CommandSpec) { s.Group = bad }},
		{"path", func(s *runtime.CommandSpec) { s.PathTpl = bad }},
		{"flag", func(s *runtime.CommandSpec) { s.Params[0].Flag = bad }},
		{"default", func(s *runtime.CommandSpec) { s.Params[0].Default = bad }},
		{"enum", func(s *runtime.CommandSpec) { s.Params[0].Enum = []string{bad} }},
		{"item-enum", func(s *runtime.CommandSpec) { s.Params[0].ItemEnum = []string{bad} }},
		{"media-type", func(s *runtime.CommandSpec) { s.RequestBody.MediaType = bad }},
		{"list-path", func(s *runtime.CommandSpec) { s.Output.ListPath = bad }},
		{"columns", func(s *runtime.CommandSpec) { s.Output.DefaultColumns = []string{bad} }},
		{"response-media-type", func(s *runtime.CommandSpec) { s.Output.ResponseMediaType = bad }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := runtime.CommandSpec{Group: "Items", Use: "list", Method: "GET", PathTpl: "/items", Params: []runtime.ParamSpec{{Flag: "id"}}, RequestBody: &runtime.RequestBody{MediaType: "application/json"}, Output: runtime.OutputHints{ListPath: "items"}}
			tc.apply(&spec)
			got := renderModuleReference(manifest, SkillModule{Specs: []runtime.CommandSpec{spec}}, true)
			for _, raw := range []string{"\n# injected", "\x00", "safe`", "injected|", "<!--"} {
				if strings.Contains(got, raw) {
					t.Errorf("unsafe metadata %q in reference:\n%s", raw, got)
				}
			}
			if !strings.Contains(got, "injected") {
				t.Fatal("metadata was discarded")
			}
		})
	}

}

func TestNamespacedPathsRejectFoldedGroupCollision(t *testing.T) {
	_, err := ResolveFlatCommandPath(config.CommandPathNamespaced, 1, []runtime.CommandSpec{
		{Group: "Users", Use: "list"}, {Group: "users", Use: "get"},
	})
	if err == nil {
		t.Fatal("case-insensitive group collision accepted")
	}
}
