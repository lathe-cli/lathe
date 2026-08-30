package overlay

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDir_EmptyDirArg(t *testing.T) {
	got, err := LoadDir("")
	if err != nil {
		t.Fatalf("LoadDir(\"\"): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestLoadDir_MissingDir(t *testing.T) {
	got, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadDir on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
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
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 modules, got %d: %v", len(got), got)
	}
	u := got["iam"].Commands["create-user"]
	if u.Use != "create" {
		t.Errorf("iam create-user use: %q", u.Use)
	}
	if u.Short != "Create a user" || u.Long == "" || u.Example == "" {
		t.Errorf("iam create-user override incomplete: %+v", u)
	}
	if len(u.Examples) != 1 || u.Examples[0].Summary != "Create a user from JSON" {
		t.Fatalf("examples = %#v", u.Examples)
	}
	if u.Examples[0].OutputHints.IDPath != "data.createUser.id" || u.Examples[0].OutputHints.ListPath != "data.users" {
		t.Errorf("example output hints = %#v", u.Examples[0].OutputHints)
	}
	if input, ok := u.Examples[0].BodyShape["input"].(map[string]any); !ok || input["email"] != "alice@example.com" {
		t.Errorf("example body shape = %#v", u.Examples[0].BodyShape)
	}
	if len(u.Examples[0].FollowUpCommands) != 1 || u.Examples[0].FollowUpCommands[0] != "myctl iam get-user --id <id> -o json" {
		t.Errorf("follow-up commands = %#v", u.Examples[0].FollowUpCommands)
	}
	if len(u.Aliases) != 2 || u.Aliases[0] != "adduser" || u.Aliases[1] != "new-user" {
		t.Errorf("iam create-user aliases: %v", u.Aliases)
	}
	if got["billing"].Commands["list-invoices"].Short != "List invoices" {
		t.Errorf("billing list-invoices: %+v", got["billing"].Commands["list-invoices"])
	}
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
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	mod := got["iam"]
	if mod.Groups["Identity"].Short != "Manage user identities" {
		t.Fatalf("group override = %#v", mod.Groups["Identity"])
	}
	if mod.Defaults.Pagination == nil {
		t.Fatal("pagination defaults were not parsed")
	}
	if len(mod.Defaults.Pagination.MatchCommands) != 2 || mod.Defaults.Pagination.MatchCommands[0] != "list-*" {
		t.Errorf("pagination match commands = %#v", mod.Defaults.Pagination.MatchCommands)
	}
	if mod.Defaults.Pagination.Params["page"] != "1" || mod.Defaults.Pagination.Params["pageSize"] != "20" {
		t.Errorf("pagination params = %#v", mod.Defaults.Pagination.Params)
	}
	cu := mod.Commands["create-user"]
	if cu.Group != "Identity" {
		t.Errorf("group = %q, want Identity", cu.Group)
	}
	if cu.Match.Method != "POST" || cu.Match.Path != "/users" {
		t.Errorf("match = %#v", cu.Match)
	}
	if cu.Hidden == nil || !*cu.Hidden {
		t.Errorf("hidden = %v, want true", cu.Hidden)
	}
	if len(cu.Notes) != 1 || cu.Notes[0] != "Use the canonical user ID." {
		t.Errorf("notes = %#v", cu.Notes)
	}
	if len(cu.Prerequisites) != 1 || cu.Prerequisites[0] != "List users before creating dependent resources." {
		t.Errorf("prerequisites = %#v", cu.Prerequisites)
	}
	if len(cu.KnownErrors) != 1 || cu.KnownErrors[0].Status != 400 || cu.KnownErrors[0].Cause != "missing user name" {
		t.Errorf("known errors = %#v", cu.KnownErrors)
	}
	if cu.Mutation != "read" {
		t.Errorf("mutation = %q, want read", cu.Mutation)
	}
	if len(cu.SearchTerms) != 2 || cu.SearchTerms[0] != "spend" || cu.SearchTerms[1] != "cost" {
		t.Errorf("search terms = %#v", cu.SearchTerms)
	}
	if cu.Context == nil || cu.Context.SetOnSuccess == nil || cu.Context.SetOnSuccess.Name != "workspace" || cu.Context.SetOnSuccess.FromParam != "status" {
		t.Errorf("context = %#v", cu.Context)
	}
	if cu.Body == nil || !cu.Body.Flags || cu.Body.RuntimeSchema == nil || cu.Body.RuntimeSchema.OperationID != "describeUser" || cu.Body.RuntimeSchema.ResponsePath != "input_schema" || cu.Body.RuntimeSchema.Params["user_id"] != "${params.user_id}" {
		t.Errorf("body override = %#v", cu.Body)
	}
	if cu.Output == nil || len(cu.Output.DefaultColumns) != 3 || cu.Output.DefaultColumns[0] != "name" || cu.Output.DefaultColumns[1] != "spendMicro" || cu.Output.DefaultColumns[2] != "status.phase" {
		t.Errorf("output = %#v", cu.Output)
	}
	if cu.Output.ColumnLabels["status.phase"] != "Status" {
		t.Errorf("column labels = %#v", cu.Output.ColumnLabels)
	}
	format := cu.Output.ColumnFormats["spendMicro"]
	if format.Kind != "currency" || format.Currency != "USD" || format.SourceScale != 6 || !format.Grouping || format.MinFractionDigits != 2 || format.MaxFractionDigits != 6 {
		t.Errorf("column format = %#v", format)
	}
	sp := cu.Params["status"]
	if sp.Flag != "user-status" {
		t.Errorf("param flag = %q, want user-status", sp.Flag)
	}
	if sp.Argument != "state" {
		t.Errorf("param argument = %q, want state", sp.Argument)
	}
	if sp.Help != "Account status" {
		t.Errorf("param help = %q, want Account status", sp.Help)
	}
	if !sp.Required {
		t.Error("param required = false, want true")
	}
	if sp.Default != "active" {
		t.Errorf("param default = %q, want active", sp.Default)
	}
	if !sp.Deprecated {
		t.Error("param deprecated = false, want true")
	}
	if sp.Context != "workspace" {
		t.Errorf("param context = %q", sp.Context)
	}
	lp := cu.Params["legacy"]
	if !lp.DeprecatedAlias {
		t.Error("legacy param hidden alias = false, want true")
	}
	du := mod.Commands["delete-user"]
	if !du.Ignore {
		t.Error("delete-user ignore = false, want true")
	}
	gu := mod.Commands["get-user"]
	if gu.Hidden == nil || *gu.Hidden {
		t.Errorf("get-user hidden = %v, want false", gu.Hidden)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
