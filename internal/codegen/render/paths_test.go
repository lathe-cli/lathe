package render

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestResolveFlatCommandPath(t *testing.T) {
	users := []runtime.CommandSpec{{Group: "Users", Use: "list-users"}}
	pets := []runtime.CommandSpec{{Group: "Pets", Use: "list-pets"}, {Group: "Pets", Use: "get-pet"}}
	duplicateGroups := []runtime.CommandSpec{{Group: "Users", Use: "list-users"}, {Group: "Users API", Use: "get-user"}}
	for _, tc := range []struct {
		name, policy string
		count        int
		specs        []runtime.CommandSpec
		flat         bool
		err          string
	}{
		{"single module", "auto", 1, users, true, ""},
		{"same group", "auto", 1, pets, true, ""},
		{"multiple modules", "auto", 2, users, false, ""},
		{"reserved search", "auto", 1, []runtime.CommandSpec{{Group: "Search", Use: "query"}}, false, ""},
		{"normalized search", "auto", 1, []runtime.CommandSpec{{Group: "Search API", Use: "query"}}, false, ""},
		{"reserved skill", "auto", 1, []runtime.CommandSpec{{Group: "Skill", Use: "install-skill"}}, false, ""},
		{"normalized groups", "auto", 1, duplicateGroups, false, ""},
		{"flat search", "flat", 1, []runtime.CommandSpec{{Group: "Search", Use: "query"}}, false, "conflicts"},
		{"flat normalized search", "flat", 1, []runtime.CommandSpec{{Group: "Search API", Use: "query"}}, false, "conflicts"},
		{"flat skill", "flat", 1, []runtime.CommandSpec{{Group: "Skill", Use: "install-skill"}}, false, "conflicts"},
		{"flat same group", "flat", 1, pets, true, ""},
		{"flat normalized groups", "flat", 1, duplicateGroups, false, "conflicts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flat, err := ResolveFlatCommandPath(tc.policy, tc.count, tc.specs)
			if tc.err != "" {
				testutil.Require(t, err != nil && strings.Contains(err.Error(), tc.err), "flat = %v, error = %v", flat, err)
			} else if err != nil || flat != tc.flat {
				t.Fatalf("flat = %v, error = %v; want %v", flat, err, tc.flat)
			}
		})
	}

	renamed := mustMergeOverlay(t, []runtime.CommandSpec{
		{Group: "Repos", Use: "create-repo", OperationID: "Repos_CreateRepo"},
		{Group: "Repos", Use: "create", OperationID: "Repos_Create"},
	}, map[string]overlay.Override{
		"create-repo": {Use: "create"},
	})
	_, err := ResolveFlatCommandPath("namespaced", 1, renamed)
	testutil.Require(t, err != nil && strings.Contains(err.Error(), `command path "repos create" conflicts`), "expected renamed command conflict error, got %v", err)

	aliased := mustMergeOverlay(t, []runtime.CommandSpec{
		{Group: "Users", Use: "get-user", OperationID: "Users_GetUser", Method: "GET", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "remove-user", OperationID: "Users_RemoveUser", Method: "DELETE", PathTpl: "/users/{id}"},
	}, map[string]overlay.Override{
		"remove-user": {Aliases: []string{"get-user"}},
	})
	_, err = ResolveFlatCommandPath("namespaced", 1, aliased)
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "alias"), "expected canonical command alias conflict error, got %v", err)
}

func TestRewriteCommandExamples_NormalizesMultiWordGroupPaths(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group:   "Payment API",
		Use:     "list-payments",
		Example: "acmectl billing payment api list-payments -o json",
		Examples: []runtime.CommandExample{{
			Command:          "acmectl billing payment api list-payments --file payment.json -o json",
			FollowUpCommands: []string{"acmectl billing payment api get-payment --id <id> -o json"},
		}},
	}}

	got := RewriteCommandExamples("acmectl", "billing", specs, true)
	testutil.Require(t, got[0].Example == "acmectl payment list-payments -o json", "flat example = %q", got[0].Example)
	testutil.Require(t, got[0].Examples[0].Command == "acmectl payment list-payments --file payment.json -o json", "flat structured example = %q", got[0].Examples[0].Command)
	testutil.Require(t, got[0].Examples[0].FollowUpCommands[0] == "acmectl billing payment api get-payment --id <id> -o json", "flat follow-up = %q", got[0].Examples[0].FollowUpCommands[0])

	got = RewriteCommandExamples("acmectl", "billing", specs, false)
	testutil.Require(t, got[0].Example == "acmectl billing payment list-payments -o json", "namespaced example = %q", got[0].Example)
	testutil.Require(t, got[0].Examples[0].Command == "acmectl billing payment list-payments --file payment.json -o json", "namespaced structured example = %q", got[0].Examples[0].Command)
}

func TestValidateModuleNames(t *testing.T) {
	testutil.NoError(t, ValidateModuleNames([]string{"pets", "billing"}))
	for _, reserved := range []string{"__lathe", "auth", "commands", "completion", "help", "login", "search", "skill", "update"} {
		err := ValidateModuleNames([]string{"pets", reserved})
		testutil.Require(t, err != nil && strings.Contains(err.Error(), "reserved root command"), "module name %q should be rejected, got %v", reserved, err)
	}
	err := ValidateModuleNames([]string{"pets", "Skill API"})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "reserved root command"), "Cobra-normalized module name should be rejected, got %v", err)
	err = ValidateModuleNames([]string{"pets", "pets"})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "mounted more than once"), "duplicate module names should be rejected, got %v", err)
	err = ValidateModuleNames([]string{"pets", "Pets API"})
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "mounted more than once"), "Cobra-normalized duplicate module names should be rejected, got %v", err)
}
