package runtime

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newSearchFixture(t *testing.T) *cobra.Command {
	t.Helper()
	root := newRootWithModuleGroup()
	mustBuild(t, root, "core", []CommandSpec{
		{Group: "Users", Use: "create-user", Short: "Create a user", OperationID: "createUser", Method: "POST", PathTpl: "/users"},
		{Group: "Users", Use: "get-user", Aliases: []string{"show-user"}, Short: "Get a user", OperationID: "getUser", Method: "GET", PathTpl: "/users/{id}",
			Params: []ParamSpec{{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true, Help: "User id"}}},
		{Group: "Users", Use: "list-users", Short: "List users", OperationID: "listUsers", Method: "GET", PathTpl: "/users"},
		{Group: "Users", Use: "update-user", Short: "Update a user", OperationID: "updateUser", Method: "PATCH", PathTpl: "/users/{id}"},
		{Group: "Users", Use: "delete-user", Short: "Delete a user", OperationID: "deleteUser", Method: "DELETE", PathTpl: "/users/{id}"},
		{Group: "Tokens", Use: "create-token", Short: "Create an API token", OperationID: "createToken", Method: "POST", PathTpl: "/tokens"},
		{Group: "Tokens", Use: "revoke-token", Short: "Revoke an API token", OperationID: "revokeToken", Method: "DELETE", PathTpl: "/tokens/{id}"},
		{Group: "Tokens", Use: "list-tokens", Short: "List API tokens", OperationID: "listTokens", Method: "GET", PathTpl: "/tokens"},
		{Group: "Widgets", Use: "list-widgets", Short: "List widgets", OperationID: "listWidgets", Method: "GET", PathTpl: "/widgets"},
		{Group: "Widgets", Use: "get-widget", Short: "Get a widget", OperationID: "getWidget", Method: "GET", PathTpl: "/widgets/{id}"},
	})
	mustBuild(t, root, "jobs", []CommandSpec{
		{Group: "Jobs", Use: "run-job", Short: "Run a job", OperationID: "runJob", Method: "POST", PathTpl: "/jobs/{id}/run"},
		{Group: "Jobs", Use: "cancel-job", Short: "Cancel a job", OperationID: "cancelJob", Method: "POST", PathTpl: "/jobs/{id}/cancel"},
		{Group: "Jobs", Use: "list-jobs", Short: "List jobs", OperationID: "listJobs", Method: "GET", PathTpl: "/jobs"},
		{Group: "Jobs", Use: "get-job-status", Short: "Get job status", OperationID: "getJobStatus", Method: "GET", PathTpl: "/jobs/{id}/status"},
		{Group: "Runners", Use: "list-runners", Short: "List runners", OperationID: "listRunners", Method: "GET", PathTpl: "/runners"},
		{Group: "Runners", Use: "register-runner", Short: "Register a runner", OperationID: "registerRunner", Method: "POST", PathTpl: "/runners"},
	})
	mustBuild(t, root, "billing", []CommandSpec{
		{Group: "Invoices", Use: "list-invoices", Short: "List invoices", OperationID: "listInvoices", Method: "GET", PathTpl: "/invoices"},
		{Group: "Invoices", Use: "download-invoice", Short: "Download an invoice", OperationID: "downloadInvoice", Method: "GET", PathTpl: "/invoices/{id}/download"},
		{Group: "Subscriptions", Use: "cancel-subscription", Short: "Cancel a subscription", OperationID: "cancelSubscription", Method: "DELETE", PathTpl: "/subscriptions/{id}"},
		{Group: "Subscriptions", Use: "list-subscriptions", Short: "List subscriptions", OperationID: "listSubscriptions", Method: "GET", PathTpl: "/subscriptions"},
	})
	return root
}

type relevanceCase struct {
	class string
	query string
	want  string
}

var relevanceCases = []relevanceCase{
	{"exact", "getUser", "core users get-user"},
	{"exact", "revoke-token", "core tokens revoke-token"},
	{"exact", "listJobs", "jobs jobs list-jobs"},
	{"exact", "show-user", "core users get-user"},
	{"exact", "/jobs/{id}/run", "jobs jobs run-job"},
	{"exact", "download invoice", "billing invoices download-invoice"},
	{"exact", "cancel subscription", "billing subscriptions cancel-subscription"},
	{"exact", "run", "jobs jobs run-job"},

	{"inflected", "revoking", "core tokens revoke-token"},
	{"inflected", "revoked token", "core tokens revoke-token"},
	{"inflected", "running", "jobs jobs run-job"},
	{"inflected", "runs", "jobs jobs run-job"},
	{"inflected", "downloading invoice", "billing invoices download-invoice"},
	{"inflected", "registering runner", "jobs runners register-runner"},
	{"inflected", "deleted user", "core users delete-user"},
	{"inflected", "listing invoices", "billing invoices list-invoices"},
	{"inflected", "canceled subscription", "billing subscriptions cancel-subscription"},
	{"inflected", "creating tokens", "core tokens create-token"},

	{"noisy", "get user by id please", "core users get-user"},
	{"noisy", "show_user stray", "core users get-user"},
	{"noisy", "cancel a running job", "jobs jobs cancel-job"},
	{"noisy", "i want to list all widgets", "core widgets list-widgets"},
}

var relevanceNoMatch = []string{"doesnotexist", "zzzz", "quantum"}

func searchPaths(root *cobra.Command, query string) []string {
	results := SearchCatalog(root, query, SearchOptions{Limit: 10})
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, strings.Join(result.Command.Path, " "))
	}
	return paths
}

func TestSearchCatalog_RelevanceBenchmark(t *testing.T) {
	root := newSearchFixture(t)

	var top1, top3 int
	var reciprocal float64
	for _, tc := range relevanceCases {
		paths := searchPaths(root, tc.query)
		rank := 0
		for i, path := range paths {
			if path == tc.want {
				rank = i + 1
				break
			}
		}
		switch {
		case rank == 1:
			top1++
			top3++
			reciprocal += 1
		case rank > 0 && rank <= 3:
			top3++
			reciprocal += 1 / float64(rank)
		case rank > 0:
			reciprocal += 1 / float64(rank)
		}
		if rank != 1 {
			t.Logf("MISS [%s] %-26q want %-38q rank=%d got=%v", tc.class, tc.query, tc.want, rank, paths)
		}
	}

	total := float64(len(relevanceCases))
	top1Rate := float64(top1) / total
	top3Rate := float64(top3) / total
	mrr := reciprocal / total
	t.Logf("relevance: cases=%d top1=%.3f top3=%.3f mrr=%.3f", len(relevanceCases), top1Rate, top3Rate, mrr)

	if top1Rate < 1 {
		t.Errorf("top-1 = %.3f, want 1.000", top1Rate)
	}
	if top3Rate < 1 {
		t.Errorf("top-3 = %.3f, want 1.000", top3Rate)
	}
	if mrr < 1 {
		t.Errorf("MRR = %.3f, want 1.000", mrr)
	}

	for _, query := range relevanceNoMatch {
		if paths := searchPaths(root, query); len(paths) != 0 {
			t.Errorf("query %q returned %v, want no results", query, paths)
		}
	}
}

func TestSearchCatalog_RejectsInfixSubstringNoise(t *testing.T) {
	root := newSearchFixture(t)

	paths := searchPaths(root, "get")
	for _, path := range paths {
		if strings.Contains(path, "widgets") && !strings.Contains(path, "get-widget") {
			t.Errorf("query %q matched %q on an infix substring", "get", path)
		}
	}
	want := []string{"core users get-user", "core widgets get-widget", "jobs jobs get-job-status"}
	for _, expected := range want {
		if !slices.Contains(paths, expected) {
			t.Errorf("query %q lost %q; got %v", "get", expected, paths)
		}
	}
}

func TestStemToken_GuardsAgainstOverStemming(t *testing.T) {
	cases := map[string]string{
		"revoking":  "revok",
		"revoked":   "revok",
		"running":   "run",
		"runs":      "run",
		"getting":   "get",
		"policies":  "policy",
		"matches":   "match",
		"processed": "process",
		"polling":   "poll",
		"run":       "run",
		"ping":      "ping",
		"bring":     "bring",
		"string":    "string",
		"used":      "used",
		"status":    "status",
		"address":   "address",
		"analysis":  "analysis",
	}
	for token, want := range cases {
		if got := stemToken(token); got != want {
			t.Errorf("stemToken(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestSearchCatalog_IdentityHitSurvivesStrongerDescriptiveMatch(t *testing.T) {
	// The summary spells "revoking" verbatim and outscores the operation-id stem
	// hit; the command must still surface on that single token.
	root := newRootWithModuleGroup()
	mustBuild(t, root, "demo", []CommandSpec{
		{Group: "Tokens", Use: "revoke-token", Short: "Revoking an API token", OperationID: "revokeToken", Method: "DELETE", PathTpl: "/tokens/{id}"},
	})

	if paths := searchPaths(root, "revoking"); !slices.Contains(paths, "demo tokens revoke-token") {
		t.Errorf("query %q returned %v", "revoking", paths)
	}
}
