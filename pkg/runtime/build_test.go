package runtime

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_RejectsExistingRootCommandConflict(t *testing.T) {
	root := newRootWithModuleGroup()
	root.AddCommand(&cobra.Command{Use: "auth"})

	err := Build(root, "auth", nil)
	testutil.Require(t, err != nil && strings.Contains(err.Error(), `module command "auth" conflicts`), "expected root conflict error, got %v", err)
	if len(root.Commands()) != 1 {
		t.Fatalf("conflicting module must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}
}

func TestBuild_RejectsParamFlagCollision(t *testing.T) {
	t.Run("across parameters", func(t *testing.T) {
		root := newRootWithModuleGroup()
		err := Build(root, "demo", []CommandSpec{{
			Group: "Users",
			Use:   "update",
			Params: []ParamSpec{
				{Name: "tokenEnv", Flag: "token-env", In: InQuery, GoType: "string"},
				{Name: "token", Flag: "token", In: InBody, GoType: "string"},
			},
		}})
		testutil.Require(t, err != nil, "expected parameter flag collision error")
	})
	t.Run("within sensitive aliases", func(t *testing.T) {
		root := newRootWithModuleGroup()
		err := Build(root, "demo", []CommandSpec{{
			Group: "Users",
			Use:   "update",
			Params: []ParamSpec{{
				Name: "token", Flag: "token", Aliases: []string{"token-env"}, In: InBody, GoType: "string",
			}},
		}})
		testutil.Require(t, err != nil, "expected sensitive alias binding collision error")
	})
}

func TestBuild_RejectsCompletionModuleName(t *testing.T) {
	root := newRootWithModuleGroup()
	if err := Build(root, "completion", nil); err == nil || !strings.Contains(err.Error(), `module command "completion" conflicts`) {
		t.Fatalf("expected completion conflict error, got %v", err)
	}
	if len(root.Commands()) != 0 {
		t.Fatalf("completion module must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}

	root = newRootWithModuleGroup()
	err := Build(root, "demo", []CommandSpec{{
		Group:     "Users",
		Use:       "get-user",
		Method:    "GET",
		PathTpl:   "/users/{id}",
		Shortcuts: []CommandShortcut{{Use: "completion"}},
	}})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "conflicts"), "expected completion shortcut conflict error, got %v", err)
}

func TestBuildFlat_RejectsCompletionGroupName(t *testing.T) {
	root := newRootWithModuleGroup()
	err := BuildFlat(root, "demo", []CommandSpec{{Group: "Completion", Use: "list", Method: "GET", PathTpl: "/x"}})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "conflicts"), "expected completion group conflict error, got %v", err)
	if len(root.Commands()) != 0 {
		t.Fatalf("completion group must not be mounted; root commands = %v", cmdNames(root.Commands()))
	}
}

func TestBuild_PopulatesGroupAndOpTree(t *testing.T) {
	specs := []CommandSpec{
		{
			Group:      "Users",
			GroupShort: "Manage user accounts",
			Use:        "get-user",
			Short:      "Get a user",
			Method:     "GET",
			PathTpl:    "/users/{id}",
			Params: []ParamSpec{
				{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: "User id"},
				{Name: "limit", Flag: "limit", In: InQuery, GoType: "int64", Help: "Page size"},
			},
		},
		{
			Group:   "Items",
			Use:     "list-items",
			Short:   "List items",
			Method:  "GET",
			PathTpl: "/items",
			Params: []ParamSpec{
				{Name: "verbose", Flag: "verbose", In: InQuery, GoType: "bool", Help: "Verbose output"},
			},
		},
	}

	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	usersGroup := mustFindChild(t, svc, "users")
	itemsGroup := mustFindChild(t, svc, "items")
	testutil.Check(t, usersGroup.Short == "Manage user accounts", "users group short = %q", usersGroup.Short)
	testutil.Check(t, itemsGroup.Short == "Items operations", "items group fallback short = %q", itemsGroup.Short)

	if len(usersGroup.Commands()) != 1 || usersGroup.Commands()[0].Use != "get-user" {
		t.Errorf("users group commands = %v, want [get-user]", cmdNames(usersGroup.Commands()))
	}
	if len(itemsGroup.Commands()) != 1 || itemsGroup.Commands()[0].Use != "list-items" {
		t.Errorf("items group commands = %v, want [list-items]", cmdNames(itemsGroup.Commands()))
	}

	getUser := usersGroup.Commands()[0]
	if f := getUser.Flag("id"); f == nil {
		t.Errorf("get-user missing --id flag")
	} else if !isRequiredFlag(f.Annotations) {
		t.Errorf("get-user --id flag is not marked required")
	}
	if f := getUser.Flag("limit"); f == nil {
		t.Errorf("get-user missing --limit flag")
	} else if f.Value.Type() != "int64" {
		t.Errorf("get-user --limit type = %q, want int64", f.Value.Type())
	}

	listItems := itemsGroup.Commands()[0]
	if f := listItems.Flag("verbose"); f == nil {
		t.Errorf("list-items missing --verbose flag")
	} else if f.Value.Type() != "bool" {
		t.Errorf("list-items --verbose type = %q, want bool", f.Value.Type())
	}
}

func TestBuild_UnknownNestedCommandIsUsage(t *testing.T) {
	for _, args := range [][]string{{"demo", "unknown"}, {"demo", "users", "unknown"}, {"demo", "users", "get-user", "unknown"}} {
		root := newRootWithModuleGroup()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		mustBuild(t, root, "demo", []CommandSpec{{Group: "Users", Use: "get-user"}})
		root.SetArgs(args)

		err := root.Execute()
		testutil.Require(t, err != nil && ClassifyError(err).Code == CodeUsage, "Execute(%v) error = %v, want usage", args, err)
	}
}

func TestAssertSchema_Match(t *testing.T) {
	testutil.NoError(t, AssertSchema(SchemaVersion))
}

func TestAssertSchema_Mismatch(t *testing.T) {
	err := AssertSchema(SchemaVersion + 999)
	testutil.Require(t, err != nil, "expected error on schema mismatch")
	testutil.Check(t, strings.Contains(err.Error(), "re-run codegen"), "unexpected error: %v", err)
}

func TestBuild_EmptySpecsMountsEmptyService(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", nil)

	svc := mustFindChild(t, root, "demo")
	if len(svc.Commands()) != 0 {
		t.Errorf("empty specs should yield no subcommands under demo; got %v", cmdNames(svc.Commands()))
	}
}

func TestBuildFlat_PopulatesRootGroupTree(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Users",
		Use:     "get-user",
		Method:  "GET",
		PathTpl: "/users/{id}",
	}}

	root := newRootWithModuleGroup()
	testutil.NoError(t, BuildFlat(root, "demo", specs))

	users := mustFindChild(t, root, "users")
	getUser := mustFindChild(t, users, "get-user")
	entry, ok := catalogCommandFromAnnotation(getUser, []string{"users", "get-user"})
	testutil.Require(t, ok, "missing catalog annotation")
	testutil.Require(t, reflect.DeepEqual(entry.Path, []string{"users", "get-user"}), "path = %#v", entry.Path)
	testutil.Require(t, entry.Service == "demo", "service = %q, want demo", entry.Service)
}

func TestBuildFlat_RejectsRootCommandConflict(t *testing.T) {
	root := newRootWithModuleGroup()
	root.AddCommand(&cobra.Command{Use: "search"})

	err := BuildFlat(root, "demo", []CommandSpec{{Group: "Search", Use: "query"}})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "conflicts"), "expected conflict error, got %v", err)
	testutil.Require(t, len(mustFindChild(t, root, "search").Commands()) == 0, "conflicting generated group should not be attached")
}

func TestBuildFlat_RejectsGeneratedGroupNameConflict(t *testing.T) {
	root := newRootWithModuleGroup()

	err := BuildFlat(root, "demo", []CommandSpec{
		{Group: "Users", Use: "list", Method: "GET", PathTpl: "/users"},
		{Group: "Users API", Use: "get", Method: "GET", PathTpl: "/users/{id}"},
	})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "conflicts"), "expected generated group conflict error, got %v", err)
	if len(root.Commands()) != 0 {
		t.Fatalf("conflicting generated groups should not be attached, got %v", cmdNames(root.Commands()))
	}
}

func TestBuild_RejectsAliasThatShadowsCanonicalCommand(t *testing.T) {
	root := newRootWithModuleGroup()
	err := Build(root, "demo", []CommandSpec{
		{Group: "Users", Use: "get-user", Method: "GET", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "remove-user", Aliases: []string{"get-user"}, Method: "DELETE", PathTpl: "/users/{id}"},
	})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "alias"), "Build error = %v, want alias conflict", err)

	root = newRootWithModuleGroup()
	err = Build(root, "demo", []CommandSpec{
		{Group: "Users API", Use: "remove-user", Aliases: []string{"get-user"}, Method: "DELETE", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "get-user", Method: "GET", PathTpl: "/users/{id}"},
	})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "alias"), "Build error = %v, want normalized group alias conflict", err)
}

func TestBuild_GroupDescriptionSummary(t *testing.T) {
	description := "\n  Manage users  \n\nDetailed guidance for user accounts."
	for _, flat := range []bool{false, true} {
		root := newRootWithModuleGroup()
		specs := []CommandSpec{{Group: "Users", GroupShort: description, Use: "list", Method: "GET", PathTpl: "/users"}}
		var err error
		if flat {
			err = BuildFlat(root, "demo", specs)
		} else {
			err = Build(root, "demo", specs)
		}
		testutil.Require(t, err == nil, "build: %v", err)
		parent := root
		if !flat {
			parent = mustFindChild(t, root, "demo")
		}
		group := mustFindChild(t, parent, "users")
		testutil.Require(t, group.Short == "Manage users", "short = %q", group.Short)
		testutil.Require(t, group.Long == description, "long = %q", group.Long)
	}
}

func TestBuild_ShortcutPresetHelp(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		for _, tc := range []struct{ help, want string }{
			{"Identifier required by the API (path, required, uuid)", "Identifier required by the API (path, uuid)"},
			{"Identifier (path, required by upstream)", "Identifier (path, required by upstream)"},
			{"Identifier (path, required by upstream) (path)", "Identifier (path, required by upstream) (path)"},
			{"Identifier (path, required by upstream) (path, required)", "Identifier (path, required by upstream) (path)"},
		} {
			root := newRootWithModuleGroup()
			spec := CommandSpec{
				Group: "Pets", Use: "get", Method: "GET", PathTpl: "/pets/{id}", Hidden: hidden,
				Params:    []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: tc.help}},
				Shortcuts: []CommandShortcut{{Use: "pet-123", Params: map[string]string{"id": "123"}}},
			}
			mustBuild(t, root, "demo", []CommandSpec{spec})
			shortcut := mustFindChild(t, root, "pet-123")
			help := shortcut.Flags().Lookup("id").Usage
			testutil.Require(t, help == tc.want, "shortcut flag help = %q, want %q", help, tc.want)
			canonical := mustFindChild(t, mustFindChild(t, mustFindChild(t, root, "demo"), "pets"), "get")
			testutil.Require(t, canonical.Flags().Lookup("id").Usage == spec.Params[0].Help, "canonical flag help changed")
		}
	}
}

func TestBuild_ShortcutOnlyVisibility(t *testing.T) {
	for _, flat := range []bool{false, true} {
		root := newRootWithModuleGroup()
		spec := CommandSpec{Group: "Identity", Use: "whoami", Short: "Inspect current identity", OperationID: "whoami", Method: "GET", PathTpl: "/whoami", Hidden: true, Shortcuts: []CommandShortcut{{Use: "whoami"}}}
		var err error
		parent := root
		path := []string{"identity", "whoami"}
		if flat {
			err = BuildFlat(root, "demo", []CommandSpec{spec})
		} else {
			err = Build(root, "demo", []CommandSpec{spec})
			parent = mustFindChild(t, root, "demo")
			path = append([]string{"demo"}, path...)
			testutil.Check(t, parent.Hidden, "fully hidden service remained visible")
		}
		testutil.NoError(t, err)
		group := mustFindChild(t, parent, "identity")
		shortcut := mustFindChild(t, root, "whoami")
		testutil.Check(t, group.Hidden && !shortcut.Hidden, "group hidden = %v, shortcut hidden = %v", group.Hidden, shortcut.Hidden)
		canonical := mustFindChild(t, group, "whoami")
		testutil.Check(t, canonical.Hidden && canonical.RunE != nil && shortcut.RunE != nil, "canonical or shortcut cannot execute")
		catalog := BuildCatalog(root, CatalogOptions{})
		testutil.Require(t, len(catalog.Commands) == 1 && reflect.DeepEqual(catalog.Commands[0].Path, []string{"whoami"}), "visible catalog = %#v", catalog.Commands)
		entry, ok := FindCatalogCommand(root, []string{"whoami"}, CatalogOptions{})
		testutil.Check(t, ok && !entry.Hidden && entry.OperationID == spec.OperationID, "shortcut detail = %#v, found = %v", entry, ok)
		_, ok = FindCatalogCommand(root, path, CatalogOptions{})
		testutil.Check(t, !ok, "hidden canonical command was exposed")
		_, ok = FindCatalogCommand(root, path, CatalogOptions{IncludeHidden: true})
		testutil.Check(t, ok, "hidden canonical command became inaccessible")
		testutil.Check(t, len(SearchCatalog(root, "whoami", SearchOptions{})) == 1, "shortcut missing from search")
	}
}

func TestBuild_MixedVisibilityGroup(t *testing.T) {
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{Group: "Identity", Use: "secret", Hidden: true},
		{Group: "Identity", Use: "whoami"},
	})
	service := mustFindChild(t, root, "demo")
	testutil.Require(t, !service.Hidden && !mustFindChild(t, service, "identity").Hidden, "mixed group or service was hidden")
}
