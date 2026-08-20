package runtime

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
)

func benchCatalogRoot(b *testing.B, resources int) *cobra.Command {
	b.Helper()
	root := &cobra.Command{Use: "benchctl"}
	root.AddGroup(&cobra.Group{ID: ModuleGroupID, Title: "Modules"})
	if err := Build(root, "demo", benchSpecs(resources)); err != nil {
		b.Fatalf("Build: %v", err)
	}
	return root
}

// BenchmarkBuildCatalog covers `<cli> commands --json`, the agent-facing
// contract walked over the whole command tree on every call.
func BenchmarkBuildCatalog(b *testing.B) {
	cases := []struct {
		name      string
		resources int
	}{
		{"small", 4},
		{"large", 48},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			root := benchCatalogRoot(b, tc.resources)
			opts := CatalogOptions{CLIName: "benchctl", CLIVersion: "0.0.0"}
			for b.Loop() {
				if catalog := BuildCatalog(root, opts); len(catalog.Commands) == 0 {
					b.Fatal("BuildCatalog produced no commands")
				}
			}
		})
	}
}

// BenchmarkCatalogJSON covers the full `commands --json` payload, including
// the serialization agents actually consume.
func BenchmarkCatalogJSON(b *testing.B) {
	root := benchCatalogRoot(b, 48)
	opts := CatalogOptions{CLIName: "benchctl", CLIVersion: "0.0.0"}
	for b.Loop() {
		data, err := json.Marshal(BuildCatalog(root, opts))
		if err != nil {
			b.Fatalf("marshal catalog: %v", err)
		}
		if len(data) == 0 {
			b.Fatal("empty catalog payload")
		}
	}
}

// BenchmarkSearchCatalog covers `<cli> search "<intent>"`, the first step of
// the agent discovery loop.
func BenchmarkSearchCatalog(b *testing.B) {
	root := benchCatalogRoot(b, 48)
	opts := SearchOptions{CatalogOptions: CatalogOptions{CLIName: "benchctl"}}
	queries := []struct {
		name  string
		query string
	}{
		{"hit", "list resource12"},
		{"miss", "provision unrelated infrastructure"},
	}

	for _, tc := range queries {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				SearchCatalog(root, tc.query, opts)
			}
		})
	}
}

// BenchmarkFindCatalogCommand covers `<cli> commands show <path...>`, the
// lookup an agent runs before executing a command.
func BenchmarkFindCatalogCommand(b *testing.B) {
	root := benchCatalogRoot(b, 48)
	opts := CatalogOptions{CLIName: "benchctl"}
	path := []string{"demo", "resource42", "get"}
	for b.Loop() {
		if _, ok := FindCatalogCommand(root, path, opts); !ok {
			b.Fatalf("FindCatalogCommand(%v) not found", path)
		}
	}
}
