package runtime

import (
	"reflect"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuildCatalog_HiddenCommands(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{Group: "Users", Use: "get-user", Short: "Get a user", Method: "GET", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "delete-user", Short: "Delete a user", Method: "DELETE", PathTpl: "/users/{id}", Hidden: true},
	})

	catalog := BuildCatalog(root, CatalogOptions{})
	testutil.Require(t, len(catalog.Commands) == 1 && catalog.Commands[0].Use == "get-user", "visible commands = %+v", catalog.Commands)
	catalog = BuildCatalog(root, CatalogOptions{IncludeHidden: true})
	testutil.Require(t, len(catalog.Commands) == 2, "all commands = %d, want 2", len(catalog.Commands))
}

func TestBuildCatalog_Capabilities(t *testing.T) {
	root := newRootWithModuleGroup()
	AttachCapability(root, CapabilitySkillBundle)
	AttachCapability(root, CapabilitySkillBundle)

	catalog := BuildCatalog(root, CatalogOptions{Capabilities: []string{"trace"}})
	testutil.Require(t, reflect.DeepEqual(catalog.CLI.Capabilities, []string{"skill.bundle", "trace"}), "capabilities = %#v", catalog.CLI.Capabilities)
}

func TestFindAndSearchCatalog(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{
			Group:       "Users",
			Use:         "get-user",
			Aliases:     []string{"show-user"},
			Short:       "Get a user",
			OperationID: "getUser",
			Method:      "GET",
			PathTpl:     "/users/{id}",
			Params:      []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: "User id"}},
		},
		{
			Group:       "Users",
			Use:         "list-users",
			Short:       "List users",
			OperationID: "listUsers",
			Method:      "GET",
			PathTpl:     "/users",
		},
	})

	cmd, ok := FindCatalogCommand(root, []string{"demo", "users", "get-user"}, CatalogOptions{})
	testutil.Require(t, ok && cmd.OperationID == "getUser", "find = %+v, %v", cmd, ok)
	cmd, ok = FindCatalogCommand(root, []string{"demo", "users", "show-user"}, CatalogOptions{})
	testutil.Require(t, ok && reflect.DeepEqual(cmd.Path, []string{"demo", "users", "get-user"}), "alias find = %+v, %v", cmd, ok)
	if _, ok := FindCatalogCommand(root, []string{"demo", "users"}, CatalogOptions{}); ok {
		t.Fatal("group container should not resolve as generated command")
	}

	for _, query := range []string{"getUser", "/users/{id}", "show-user", "id"} {
		results := SearchCatalog(root, query, SearchOptions{Limit: 10})
		testutil.Require(t, len(results) != 0 && results[0].Command.Use == "get-user", "query %q results = %+v", query, results)
	}

	results := SearchCatalog(root, "users", SearchOptions{Limit: 1})
	testutil.Require(t, len(results) == 1, "limited results = %d, want 1", len(results))
}

func TestSearchCatalog_SoftMatchesNoisyIntent(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{
			Group:       "Users",
			Use:         "get-user",
			Aliases:     []string{"show-user"},
			Short:       "Get a user",
			OperationID: "getUser",
			Method:      "GET",
			PathTpl:     "/users/{id}",
		},
		{
			Group:       "Users",
			Use:         "list-users",
			Short:       "List users",
			OperationID: "listUsers",
			Method:      "GET",
			PathTpl:     "/users",
		},
	})

	results := SearchCatalog(root, "get user stray", SearchOptions{Limit: 10})
	testutil.Require(t, len(results) != 0 && results[0].Command.Use == "get-user", "noisy get user results = %+v", results)

	results = SearchCatalog(root, "show_user stray", SearchOptions{Limit: 10})
	testutil.Require(t, len(results) != 0 && results[0].Command.Use == "get-user", "normalized alias results = %+v", results)

	results = SearchCatalog(root, "doesnotexist", SearchOptions{Limit: 10})
	testutil.Require(t, len(results) == 0, "unknown query results = %+v", results)
}

func TestAttachCatalogCommand_DoesNotClaimPreviewWithoutRuntime(t *testing.T) {
	root := newRootWithModuleGroup()
	cmd := helpCommand("package", "Package skill")
	cmd.Flags().Bool("dry-run", false, "")
	AttachCatalogCommand(cmd, "console-rest", CommandSpec{
		Group:   "Skills",
		Use:     "package",
		Method:  "POST",
		PathTpl: "/skills/package",
	})
	root.AddCommand(cmd)

	got := BuildCatalog(root, CatalogOptions{}).Commands[0]
	testutil.Require(t, got.DryRun != nil && got.DryRun.Mode == DryRunUnsupported && got.DryRun.Flag == "", "dry_run = %+v", got.DryRun)
	if WiredDryRunFlag(cmd) != "" {
		t.Fatalf("wired flag = %q", WiredDryRunFlag(cmd))
	}
}

func TestBuildCatalog_DryRunFlagCollision(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Method:  "GET",
		PathTpl: "/users/{id}",
		Params:  []ParamSpec{{Name: "dry-run", Flag: "dry-run", In: InQuery, GoType: "string"}},
	}})
	cmd := BuildCatalog(root, CatalogOptions{}).Commands[0]
	testutil.Require(t, cmd.DryRun != nil && cmd.DryRun.Flag == "lathe-dry-run", "dry_run = %+v", cmd.DryRun)
	found, ok := FindCatalogCommand(root, cmd.Path, CatalogOptions{})
	testutil.Require(t, ok, "command not found")
	cobraCmd := findChildCommand(findChildCommand(findChildCommand(root, cmd.Path[0]), cmd.Path[1]), cmd.Path[2])
	testutil.Require(t, cobraCmd != nil && cobraCmd.Flags().Lookup(found.DryRun.Flag) != nil, "cobra missing --%s", found.DryRun.Flag)
}

func TestBuildCatalog_DefaultAuthRequired(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Short:   "Get a user",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}})

	catalog := BuildCatalog(root, CatalogOptions{})
	testutil.Require(t, len(catalog.Commands) == 1, "commands = %d, want 1", len(catalog.Commands))
	testutil.Require(t, catalog.Commands[0].Auth.Required, "nil security should require auth to match runtime behavior")
}
