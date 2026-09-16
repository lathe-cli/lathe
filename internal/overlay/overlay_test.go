package overlay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestLoadDir_EmptyDirArg(t *testing.T) {
	got, err := LoadDir("")
	testutil.Require(t, err == nil, "LoadDir(\"\"): %v", err)
	testutil.Check(t, len(got) == 0, "want empty map, got %v", got)
}

func TestLoadDir_MissingDir(t *testing.T) {
	got, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist"))
	testutil.Require(t, err == nil, "LoadDir on missing dir: %v", err)
	testutil.Check(t, len(got) == 0, "want empty map, got %v", got)
}

func TestLoadDir_ParsesMultipleModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "iam.yaml"), `commands:
  create-user:
    use: create
    aliases: [adduser, new-user]
    short: "Create a user"
    long: "Long description for create-user."
    example: "myctl iam create-user --email a@b.c"
    examples:
      - summary: "Create a user from JSON"
        command: "myctl iam create-user --file user.json -o json"
        body_shape:
          input:
            email: "alice@example.com"
        output_hints:
          id_path: "data.createUser.id"
          list_path: "data.users"
        follow_up_commands:
          - "myctl iam get-user --id <id> -o json"
`)
	writeFile(t, filepath.Join(dir, "billing.yaml"), `commands:
  list-invoices:
    short: "List invoices"
`)
	writeFile(t, filepath.Join(dir, "README.md"), "should be ignored")

	got, err := LoadDir(dir)
	testutil.Require(t, err == nil, "LoadDir: %v", err)
	testutil.Require(t, len(got) == 2, "want 2 modules, got %d: %v", len(got), got)
	u := got["iam"].Commands["create-user"]
	testutil.Check(t, u.Use == "create", "iam create-user use: %q", u.Use)
	testutil.Check(t, u.Short == "Create a user" && u.Long != "" && u.Example != "", "iam create-user override incomplete: %+v", u)
	testutil.Require(t, len(u.Examples) == 1 && u.Examples[0].Summary == "Create a user from JSON", "examples = %#v", u.Examples)
	testutil.Check(t, u.Examples[0].OutputHints.IDPath == "data.createUser.id" && u.Examples[0].OutputHints.ListPath == "data.users", "example output hints = %#v", u.Examples[0].OutputHints)
	if input, ok := u.Examples[0].BodyShape["input"].(map[string]any); !ok || input["email"] != "alice@example.com" {
		t.Errorf("example body shape = %#v", u.Examples[0].BodyShape)
	}
	testutil.Check(t, len(u.Examples[0].FollowUpCommands) == 1 && u.Examples[0].FollowUpCommands[0] == "myctl iam get-user --id <id> -o json", "follow-up commands = %#v", u.Examples[0].FollowUpCommands)
	testutil.Check(t, len(u.Aliases) == 2 && u.Aliases[0] == "adduser" && u.Aliases[1] == "new-user", "iam create-user aliases: %v", u.Aliases)
	testutil.Check(t, got["billing"].Commands["list-invoices"].Short == "List invoices", "billing list-invoices: %+v", got["billing"].Commands["list-invoices"])
}

func TestLoadDir_ParsesExtendedFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "iam.yaml"), `defaults:
  pagination:
    match_commands: ["list-*", "query-*"]
    params:
      page: "1"
      pageSize: "20"
groups:
  Identity:
    short: "Manage user identities"
commands:
  create-user:
    match:
      method: POST
      path: /users
    group: "Identity"
    hidden: true
    notes:
      - "Use the canonical user ID."
    prerequisites:
      - "List users before creating dependent resources."
    known_errors:
      - status: 400
        cause: "missing user name"
    mutation: read
    search_terms: [spend, cost]
    context:
      set_on_success:
        name: workspace
        from_param: status
    body:
      flags: true
      runtime_schema:
        operation_id: describeUser
        response_path: input_schema
        params:
          user_id: ${params.user_id}
    output:
      default_columns: [name, spendMicro, status.phase]
      column_labels:
        status.phase: Status
      column_formats:
        spendMicro:
          kind: currency
          currency: USD
          source_scale: 6
          grouping: true
          min_fraction_digits: 2
          max_fraction_digits: 6
      column_alignments:
        spendMicro: right
    params:
      status:
        flag: user-status
        argument: state
        help: "Account status"
        required: true
        default: "active"
        deprecated: true
        context: workspace
      legacy:
        hidden: true
  delete-user:
    ignore: true
  get-user:
    hidden: false
`)
	got, err := LoadDir(dir)
	testutil.Require(t, err == nil, "LoadDir: %v", err)
	mod := got["iam"]
	testutil.Require(t, mod.Groups["Identity"].Short == "Manage user identities", "group override = %#v", mod.Groups["Identity"])
	testutil.Require(t, mod.Defaults.Pagination != nil, "pagination defaults were not parsed")
	testutil.Check(t, len(mod.Defaults.Pagination.MatchCommands) == 2 && mod.Defaults.Pagination.MatchCommands[0] == "list-*", "pagination match commands = %#v", mod.Defaults.Pagination.MatchCommands)
	testutil.Check(t, mod.Defaults.Pagination.Params["page"] == "1" && mod.Defaults.Pagination.Params["pageSize"] == "20", "pagination params = %#v", mod.Defaults.Pagination.Params)
	cu := mod.Commands["create-user"]
	testutil.Check(t, cu.Group == "Identity", "group = %q, want Identity", cu.Group)
	testutil.Check(t, cu.Match.Method == "POST" && cu.Match.Path == "/users", "match = %#v", cu.Match)
	testutil.Check(t, cu.Hidden != nil && *cu.Hidden, "hidden = %v, want true", cu.Hidden)
	testutil.Check(t, len(cu.Notes) == 1 && cu.Notes[0] == "Use the canonical user ID.", "notes = %#v", cu.Notes)
	testutil.Check(t, len(cu.Prerequisites) == 1 && cu.Prerequisites[0] == "List users before creating dependent resources.", "prerequisites = %#v", cu.Prerequisites)
	testutil.Check(t, len(cu.KnownErrors) == 1 && cu.KnownErrors[0].Status == 400 && cu.KnownErrors[0].Cause == "missing user name", "known errors = %#v", cu.KnownErrors)
	testutil.Check(t, cu.Mutation == "read", "mutation = %q, want read", cu.Mutation)
	testutil.Check(t, len(cu.SearchTerms) == 2 && cu.SearchTerms[0] == "spend" && cu.SearchTerms[1] == "cost", "search terms = %#v", cu.SearchTerms)
	testutil.Check(t, cu.Context != nil && cu.Context.SetOnSuccess != nil && cu.Context.SetOnSuccess.Name == "workspace" && cu.Context.SetOnSuccess.FromParam == "status", "context = %#v", cu.Context)
	testutil.Check(t, cu.Body != nil && cu.Body.Flags && cu.Body.RuntimeSchema != nil && cu.Body.RuntimeSchema.OperationID == "describeUser" && cu.Body.RuntimeSchema.ResponsePath == "input_schema" && cu.Body.RuntimeSchema.Params["user_id"] == "${params.user_id}", "body override = %#v", cu.Body)
	testutil.Check(t, cu.Output != nil && len(cu.Output.DefaultColumns) == 3 && cu.Output.DefaultColumns[0] == "name" && cu.Output.DefaultColumns[1] == "spendMicro" && cu.Output.DefaultColumns[2] == "status.phase", "output = %#v", cu.Output)
	testutil.Check(t, cu.Output.ColumnLabels["status.phase"] == "Status", "column labels = %#v", cu.Output.ColumnLabels)
	format := cu.Output.ColumnFormats["spendMicro"]
	testutil.Check(t, format.Kind == "currency" && format.Currency == "USD" && format.SourceScale == 6 && format.Grouping && format.MinFractionDigits == 2 && format.MaxFractionDigits == 6, "column format = %#v", format)
	testutil.Check(t, cu.Output.ColumnAlignments["spendMicro"] == "right", "column alignments = %#v", cu.Output.ColumnAlignments)
	sp := cu.Params["status"]
	testutil.Check(t, sp.Flag == "user-status", "param flag = %q, want user-status", sp.Flag)
	testutil.Check(t, sp.Argument == "state", "param argument = %q, want state", sp.Argument)
	testutil.Check(t, sp.Help == "Account status", "param help = %q, want Account status", sp.Help)
	testutil.Check(t, sp.Required, "param required = false, want true")
	testutil.Check(t, sp.Default == "active", "param default = %q, want active", sp.Default)
	testutil.Check(t, sp.Deprecated, "param deprecated = false, want true")
	testutil.Check(t, sp.Context == "workspace", "param context = %q", sp.Context)
	lp := cu.Params["legacy"]
	testutil.Check(t, lp.DeprecatedAlias, "legacy param hidden alias = false, want true")
	du := mod.Commands["delete-user"]
	testutil.Check(t, du.Ignore, "delete-user ignore = false, want true")
	gu := mod.Commands["get-user"]
	testutil.Check(t, gu.Hidden != nil && !*gu.Hidden, "get-user hidden = %v, want false", gu.Hidden)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}
