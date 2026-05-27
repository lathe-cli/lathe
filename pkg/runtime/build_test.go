package runtime

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newRootWithModuleGroup() *cobra.Command {
	root := &cobra.Command{Use: "myctl"}
	root.AddGroup(&cobra.Group{ID: ModuleGroupID, Title: "Modules"})
	return root
}

func TestBuild_PopulatesGroupAndOpTree(t *testing.T) {
	specs := []CommandSpec{
		{
			Group:   "Users",
			Use:     "get-user",
			Short:   "Get a user",
			Method:  "GET",
			PathTpl: "/users/{id}",
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
	Build(root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	usersGroup := mustFindChild(t, svc, "users")
	itemsGroup := mustFindChild(t, svc, "items")

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

func TestAssertSchema_Match(t *testing.T) {
	if err := AssertSchema(SchemaVersion); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertSchema_Mismatch(t *testing.T) {
	err := AssertSchema(SchemaVersion + 999)
	if err == nil {
		t.Fatal("expected error on schema mismatch")
	}
	if !strings.Contains(err.Error(), "re-run codegen") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuild_EmptySpecsMountsEmptyService(t *testing.T) {
	root := newRootWithModuleGroup()
	Build(root, "demo", nil)

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
	if err := BuildFlat(root, "demo", specs); err != nil {
		t.Fatalf("BuildFlat: %v", err)
	}

	users := mustFindChild(t, root, "users")
	getUser := mustFindChild(t, users, "get-user")
	entry, ok := catalogCommandFromAnnotation(getUser, []string{"users", "get-user"})
	if !ok {
		t.Fatal("missing catalog annotation")
	}
	if !reflect.DeepEqual(entry.Path, []string{"users", "get-user"}) {
		t.Fatalf("path = %#v", entry.Path)
	}
	if entry.Service != "demo" {
		t.Fatalf("service = %q, want demo", entry.Service)
	}
}

func TestBuildFlat_RejectsRootCommandConflict(t *testing.T) {
	root := newRootWithModuleGroup()
	root.AddCommand(&cobra.Command{Use: "search"})

	err := BuildFlat(root, "demo", []CommandSpec{{Group: "Search", Use: "query"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected conflict error, got %v", err)
	}
	if len(mustFindChild(t, root, "search").Commands()) != 0 {
		t.Fatal("conflicting generated group should not be attached")
	}
}

func TestBuildFlat_RejectsGeneratedGroupNameConflict(t *testing.T) {
	root := newRootWithModuleGroup()

	err := BuildFlat(root, "demo", []CommandSpec{
		{Group: "Users", Use: "list", Method: "GET", PathTpl: "/users"},
		{Group: "Users API", Use: "get", Method: "GET", PathTpl: "/users/{id}"},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected generated group conflict error, got %v", err)
	}
	if len(root.Commands()) != 0 {
		t.Fatalf("conflicting generated groups should not be attached, got %v", cmdNames(root.Commands()))
	}
}

func TestBuild_BodyFlagsAttachedWhenHasBody(t *testing.T) {
	specs := []CommandSpec{{
		Group:       "Users",
		Use:         "create-user",
		Method:      "POST",
		PathTpl:     "/users",
		RequestBody: &RequestBody{Required: true},
	}}

	root := newRootWithModuleGroup()
	Build(root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	users := mustFindChild(t, svc, "users")
	createUser := mustFindChild(t, users, "create-user")

	for _, name := range []string{"file", "set", "set-str"} {
		if createUser.Flag(name) == nil {
			t.Errorf("create-user missing --%s flag", name)
		}
	}
}

func TestBuild_SetStrSendsStringBodyFields(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		rawBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:       "Users",
		Use:         "create-user",
		Method:      "POST",
		PathTpl:     "/users",
		RequestBody: &RequestBody{Required: true},
		Security:    &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	Build(root, "demo", specs)
	root.SetArgs([]string{
		"--hostname", srv.URL,
		"demo", "users", "create-user",
		"--set", "spec.replicas=3",
		"--set", "spec.enabled=true",
		"--set-str", "spec.stringReplicas=3",
		"--set-str", "spec.stringEnabled=true",
		"--set-str", "spec.csv=a,b",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(rawBody, &got); err != nil {
		t.Fatalf("invalid request JSON %q: %v", string(rawBody), err)
	}
	want := map[string]any{
		"spec": map[string]any{
			"replicas":       float64(3),
			"enabled":        true,
			"stringReplicas": "3",
			"stringEnabled":  "true",
			"csv":            "a,b",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestBuild_RequiredQueryParamBlocksBeforeRequest(t *testing.T) {
	bindTestManifest(t, "myctl", "MYCTL_HOST")
	t.Setenv("MYCTL_CONFIG_DIR", t.TempDir())

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	specs := []CommandSpec{{
		Group:   "Receivers",
		Use:     "get-receiver",
		Method:  "GET",
		PathTpl: "/receivers",
		Params: []ParamSpec{
			{Name: "type", Flag: "type", In: InQuery, GoType: "string", Required: true, Help: "Receiver type"},
		},
		Security: &SecurityHint{Public: true},
	}}

	root := newRootWithModuleGroup()
	root.PersistentFlags().String("hostname", "", "")
	root.PersistentFlags().StringP("output", "o", "raw", "")
	Build(root, "demo", specs)
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "receivers", "get-receiver"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected required flag error")
	}
	if !strings.Contains(err.Error(), "required flag") || !strings.Contains(err.Error(), "type") {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != 0 {
		t.Fatalf("server hits = %d, want 0", hits)
	}
}

func TestBuild_PaginationFlagsAttached(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Items",
		Use:     "list-items",
		Method:  "GET",
		PathTpl: "/items",
		Output: OutputHints{
			Pagination: &PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token"},
		},
	}}

	root := newRootWithModuleGroup()
	Build(root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	items := mustFindChild(t, svc, "items")
	listItems := mustFindChild(t, items, "list-items")

	for _, name := range []string{"all", "max-pages"} {
		if listItems.Flag(name) == nil {
			t.Errorf("list-items missing --%s flag", name)
		}
	}
}

func TestBuild_WaitFlagOnMutating(t *testing.T) {
	specs := []CommandSpec{
		{Group: "Resources", Use: "create-resource", Method: "POST", PathTpl: "/resources"},
		{Group: "Resources", Use: "list-resources", Method: "GET", PathTpl: "/resources"},
	}

	root := newRootWithModuleGroup()
	Build(root, "demo", specs)

	svc := mustFindChild(t, root, "demo")
	resources := mustFindChild(t, svc, "resources")

	create := mustFindChild(t, resources, "create-resource")
	if create.Flag("wait") == nil {
		t.Error("create-resource (POST) should have --wait flag")
	}

	list := mustFindChild(t, resources, "list-resources")
	if list.Flag("wait") != nil {
		t.Error("list-resources (GET) should NOT have --wait flag")
	}
}

func mustFindChild(t *testing.T, parent *cobra.Command, use string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Use == use {
			return c
		}
	}
	t.Fatalf("%s has no child %q; children = %v", parent.Use, use, cmdNames(parent.Commands()))
	return nil
}

func cmdNames(cmds []*cobra.Command) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Use)
	}
	return names
}

func isRequiredFlag(annotations map[string][]string) bool {
	for _, v := range annotations[cobra.BashCompOneRequiredFlag] {
		if v == "true" {
			return true
		}
	}
	return false
}
