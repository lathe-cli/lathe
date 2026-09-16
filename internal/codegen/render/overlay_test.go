package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func mustMergeOverlay(t *testing.T, specs []runtime.CommandSpec, overrides map[string]overlay.Override) []runtime.CommandSpec {
	t.Helper()
	merged, err := MergeOverlay(specs, overrides)
	testutil.Require(t, err == nil, "%v", err)
	return merged
}

func mustMergeOverlayModule(t *testing.T, specs []runtime.CommandSpec, mod overlay.Module) []runtime.CommandSpec {
	t.Helper()
	merged, err := MergeOverlayModule(specs, mod)
	testutil.Require(t, err == nil, "%v", err)
	return merged
}

func TestValidateOverlayModule_RejectsInvalidGroups(t *testing.T) {
	specs := []runtime.CommandSpec{{Group: "Users", Use: "list-users"}}
	for _, tc := range []struct {
		name  string
		group string
		short string
		want  string
	}{
		{name: "unknown", group: "Missing", short: "Manage missing resources", want: `group "Missing" does not exist`},
		{name: "empty short", group: "Users", want: `group "Users" short must be one non-empty trimmed line`},
		{name: "multiline short", group: "Users", short: "Manage\nusers", want: `group "Users" short must be one non-empty trimmed line`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOverlayModule(specs, overlay.Module{Groups: map[string]overlay.GroupOverride{
				tc.group: {Short: tc.short},
			}})
			testutil.Require(t, err != nil && strings.Contains(err.Error(), tc.want), "validation error = %v", err)
		})
	}
}

func TestOverlayModule_RejectsInvalidMutationOverride(t *testing.T) {
	specs := []runtime.CommandSpec{{Group: "Reports", Use: "query-report", Method: "POST", PathTpl: "/reports/query"}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"query-report": {Mutation: "maybe"},
	}}
	wantMsg := `mutation must be "read" or "write"`
	if err := ValidateOverlayModule(specs, mod); err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("validate error = %v", err)
	}
	if _, err := MergeOverlayModule(specs, mod); err == nil || !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("merge error = %v", err)
	}
}

func TestMergeOverlayModule_IgnoresCollisionBeforeDisambiguation(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/v1/groups"},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/v2/groups"},
	}})
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"list": {Match: overlay.OperationMatch{Path: "/v2/groups"}, Ignore: true},
	}}
	merged := mustMergeOverlayModule(t, specs, mod)
	testutil.Require(t, len(merged) == 1, "merged command count = %d, want 1", len(merged))
	if got := merged[0].Use; got != "list" {
		t.Fatalf("surviving Use = %q, want list", got)
	}
	if got := merged[0].PathTpl; got != "/v1/groups" {
		t.Fatalf("surviving path = %q, want /v1/groups", got)
	}
}

func TestMergeOverlayModule_PreservesLegacyCollisionOverlayKey(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/v1/groups"},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/v2/groups"},
	}})

	t.Run("ignore", func(t *testing.T) {
		mod := overlay.Module{Commands: map[string]overlay.Override{
			"list-2": {Match: overlay.OperationMatch{Path: "/v2/groups"}, Ignore: true},
		}}
		testutil.NoError(t, ValidateOverlayModule(specs, mod))
		merged := mustMergeOverlayModule(t, specs, mod)
		testutil.Require(t, len(merged) == 1 && merged[0].PathTpl == "/v1/groups" && merged[0].Use == "list", "merged = %#v, want only /v1/groups as list", merged)
	})

	t.Run("override", func(t *testing.T) {
		mod := overlay.Module{Commands: map[string]overlay.Override{
			"list-2": {Match: overlay.OperationMatch{Path: "/v2/groups"}, Short: "Legacy second command"},
		}}
		testutil.NoError(t, ValidateOverlayModule(specs, mod))
		merged := mustMergeOverlayModule(t, specs, mod)
		testutil.Require(t, len(merged) == 2 && merged[0].Short != "Legacy second command" && merged[1].Short == "Legacy second command", "merged = %#v, want override only on second command", merged)
	})

	t.Run("unsuffixed override remains first", func(t *testing.T) {
		merged := mustMergeOverlayModule(t, specs, overlay.Module{Commands: map[string]overlay.Override{
			"list": {Short: "First command only"},
		}})
		testutil.Require(t, len(merged) == 2 && merged[0].Short == "First command only" && merged[1].Short != "First command only", "merged = %#v, want unsuffixed override only on first command", merged)
	})
}

func TestMergeOverlayModule_DisambiguatesSurvivingCollisions(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/Groups"},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/groups"},
	}})
	merged := mustMergeOverlayModule(t, specs, overlay.Module{})
	testutil.Require(t, len(merged) == 2, "merged command count = %d, want 2", len(merged))
	testutil.Require(t, merged[0].Use != merged[1].Use, "colliding commands both use %q", merged[0].Use)
}

func TestValidateOverlayModule_RejectsUnknownArgumentParameter(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "Users", Use: "get-user", Params: []runtime.ParamSpec{{Name: "id", Flag: "id"}},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"get-user": {Params: map[string]overlay.ParamOverride{"missing": {Argument: "id"}}},
	}}

	err := ValidateOverlayModule(specs, mod)
	testutil.Require(t, err != nil && strings.Contains(err.Error(), `argument parameter "missing" does not exist`), "validation error = %v", err)
}

func TestMergeOverlay_ParamRequiredOverride(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "get-user", Short: "get", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "type", Flag: "type", In: "query", GoType: "string", Help: "original help"},
			},
		},
	}

	merged := mustMergeOverlay(t, specs, map[string]overlay.Override{
		"get-user": {
			Params: map[string]overlay.ParamOverride{
				"type":    {Required: true, Help: "override help"},
				"missing": {Required: true},
			},
		},
	})

	testutil.Require(t, len(merged) == 1, "merged specs = %d, want 1", len(merged))
	testutil.Require(t, len(merged[0].Params) == 1, "params = %d, want 1", len(merged[0].Params))
	param := merged[0].Params[0]
	testutil.Require(t, param.Required, "required = false, want true")
	testutil.Require(t, param.Help == "override help", "help = %q, want override help", param.Help)
}

func TestMergeOverlay_UseRename(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group:   "Repos",
		Use:     "create-repo",
		Aliases: []string{"new-repo"},
	}}
	merged := mustMergeOverlay(t, specs, map[string]overlay.Override{
		"create-repo": {Use: "create", Aliases: []string{"new"}},
	})

	if got := merged[0].Use; got != "create" {
		t.Fatalf("Use = %q, want create", got)
	}
	testutil.Require(t, reflect.DeepEqual(merged[0].Aliases, []string{"new-repo", "new"}), "aliases = %#v", merged[0].Aliases)
}

func TestMergeOverlay_PathScopedRename(t *testing.T) {
	specs := []runtime.CommandSpec{
		{Group: "GatewayService", Use: "create-service", OperationID: "GatewayService_CreateService", Method: "POST", PathTpl: "/apis/v1alpha1/services"},
		{Group: "GatewayService", Use: "create-service", OperationID: "GatewayService_CreateService", Method: "POST", PathTpl: "/apis/v1alpha2/services"},
	}
	_, err := ResolveFlatCommandPath("namespaced", 1, specs)
	testutil.Require(t, err != nil && strings.Contains(err.Error(), "/apis/v1alpha1/services") && strings.Contains(err.Error(), "/apis/v1alpha2/services"), "conflict error = %v", err)

	merged := mustMergeOverlay(t, specs, map[string]overlay.Override{
		"create-service": {Match: overlay.OperationMatch{Method: "POST", Path: "/apis/v1alpha2/services"}, Use: "create-service-v1alpha2"},
	})
	testutil.Require(t, merged[0].Use == "create-service" && merged[1].Use == "create-service-v1alpha2", "uses = %q, %q", merged[0].Use, merged[1].Use)
	if _, err := ResolveFlatCommandPath("namespaced", 1, merged); err != nil {
		t.Fatalf("renamed command should not conflict: %v", err)
	}
}

func TestMergeOverlayModule_BulkPaginationDefaults(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "list-users", Short: "list users", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "page", Flag: "page", In: "query", GoType: "string"},
				{Name: "pageSize", Flag: "page-size", In: "query", GoType: "string"},
			},
		},
		{
			Group: "Users", Use: "query-users", Short: "query users", Method: "GET", PathTpl: "/users/query",
			Params: []runtime.ParamSpec{
				{Name: "page", Flag: "page", In: "query", GoType: "string"},
				{Name: "pageSize", Flag: "page-size", In: "query", GoType: "string"},
			},
		},
		{
			Group: "Users", Use: "get-user", Short: "get user", Method: "GET", PathTpl: "/users/{id}",
			Params: []runtime.ParamSpec{
				{Name: "id", Flag: "id", In: "path", GoType: "string"},
			},
		},
	}

	bulk := mustMergeOverlayModule(t, specs, overlay.Module{
		Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
			MatchCommands: []string{"list-*", "query-*"},
			Params:        map[string]string{"page": "1", "pageSize": "20"},
		}},
		Commands: map[string]overlay.Override{
			"query-users": {Params: map[string]overlay.ParamOverride{"page": {Default: "7"}}},
		},
	})
	explicit := mustMergeOverlay(t, specs, map[string]overlay.Override{
		"list-users": {
			Params: map[string]overlay.ParamOverride{
				"page":     {Default: "1"},
				"pageSize": {Default: "20"},
			},
		},
		"query-users": {
			Params: map[string]overlay.ParamOverride{
				"page":     {Default: "7"},
				"pageSize": {Default: "20"},
			},
		},
	})

	testutil.Require(t, reflect.DeepEqual(bulk, explicit), "bulk defaults differ from explicit overrides:\nbulk: %#v\nexplicit: %#v", bulk, explicit)
	if got := paramDefault(t, bulk, "get-user", "id"); got != "" {
		t.Fatalf("non-matching command default = %q, want empty", got)
	}
}

func TestMergeOverlayModule_BulkDefaultsUseLegacyCollisionName(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Groups", OperationID: "Groups_List", Method: "GET", Path: "/v1/groups", Parameters: []rawir.RawParameter{{Name: "page", In: "query", Type: "integer"}}},
		{Group: "Groups", OperationID: "Groups_list", Method: "GET", Path: "/v2/groups", Parameters: []rawir.RawParameter{{Name: "page", In: "query", Type: "integer"}}},
	}})
	merged := mustMergeOverlayModule(t, specs, overlay.Module{Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
		MatchCommands: []string{"list-2"},
		Params:        map[string]string{"page": "2"},
	}}})
	testutil.Require(t, len(merged) == 2 && merged[0].Params[0].Default == "" && merged[1].Params[0].Default == "2", "merged = %#v, want bulk default only on list-2", merged)
}

func TestMergeOverlayModule_BulkDefaultsDoNotReplaceSpecDefaults(t *testing.T) {
	specs := []runtime.CommandSpec{
		{
			Group: "Users", Use: "list-users", Short: "list users", Method: "GET", PathTpl: "/users",
			Params: []runtime.ParamSpec{
				{Name: "page", Flag: "page", In: "query", GoType: "string", Default: "5"},
				{Name: "pageSize", Flag: "page-size", In: "query", GoType: "string"},
			},
		},
	}

	merged := mustMergeOverlayModule(t, specs, overlay.Module{
		Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
			MatchCommands: []string{"list-*"},
			Params:        map[string]string{"page": "1", "pageSize": "20"},
		}},
		Commands: map[string]overlay.Override{
			"list-users": {Params: map[string]overlay.ParamOverride{"page": {Default: "9"}}},
		},
	})

	if got := paramDefault(t, merged, "list-users", "page"); got != "9" {
		t.Fatalf("per-command default = %q, want 9", got)
	}
	if got := paramDefault(t, merged, "list-users", "pageSize"); got != "20" {
		t.Fatalf("bulk pageSize default = %q, want 20", got)
	}

	withoutCommandOverride := mustMergeOverlayModule(t, specs, overlay.Module{
		Defaults: overlay.Defaults{Pagination: &overlay.PaginationDefaults{
			MatchCommands: []string{"list-*"},
			Params:        map[string]string{"page": "1"},
		}},
	})
	if got := paramDefault(t, withoutCommandOverride, "list-users", "page"); got != "5" {
		t.Fatalf("spec default = %q, want 5", got)
	}
}

func TestMergeOverlayModule_StreamPolicy(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "Runs", Use: "run", Method: "POST", PathTpl: "/runs",
		Output: runtime.OutputHints{Streaming: &runtime.StreamingHint{Strategy: "sse"}},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"run": {Output: &overlay.OutputOverride{Streaming: &overlay.StreamingOverride{
			Data: "json", EventNamePath: "kind",
			Collect: &overlay.StreamCollect{
				RequireStop: true, StopEvents: []string{"done"},
				Fields: []overlay.StreamFieldRule{{Events: []string{"chunk"}, From: "text", To: "answer", Reduce: "concat"}},
			},
			Live: &overlay.StreamLive{Events: []string{"chunk"}, From: "text"},
		}}},
	}}
	testutil.NoError(t, ValidateOverlayModule(specs, mod))
	merged := mustMergeOverlayModule(t, specs, mod)
	policy := merged[0].Output.Streaming.Policy
	testutil.Require(t, policy != nil && policy.Collect != nil && policy.Collect.Fields[0].Reduce == "concat" && policy.Live != nil, "policy = %#v", policy)
	testutil.Require(t, specs[0].Output.Streaming.Policy == nil, "merge mutated source spec")

	bad := specs
	bad[0].Output.Streaming = nil
	if err := ValidateOverlayModule(bad, mod); err == nil || !strings.Contains(err.Error(), "not declared as streaming") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestMergeOverlayModule_OutputColumns(t *testing.T) {
	specs := []runtime.CommandSpec{{
		Group: "Resources", Use: "list", Method: "GET", PathTpl: "/resources",
		Output: runtime.OutputHints{ListPath: "items", DefaultColumns: []string{"id", "category"}},
	}}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"list": {Output: &overlay.OutputOverride{
			DefaultColumns: []string{"resourceId", "displayName", "spendMicro"},
			ColumnLabels:   map[string]string{"resourceId": "Resource ID", "displayName": "Name"},
			ColumnFormats: map[string]overlay.ColumnFormatOverride{
				"spendMicro": {Kind: "currency", Currency: "USD", SourceScale: 6, Grouping: true, MinFractionDigits: 2, MaxFractionDigits: 6},
			},
			ColumnAlignments: map[string]string{"spendMicro": "right", "resourceId": "left"},
		}},
	}}
	testutil.NoError(t, ValidateOverlayModule(specs, mod))
	merged := mustMergeOverlayModule(t, specs, mod)
	if got := strings.Join(merged[0].Output.DefaultColumns, ","); got != "resourceId,displayName,spendMicro" {
		t.Fatalf("default columns = %q", got)
	}
	if got := merged[0].Output.ColumnLabels["resourceId"]; got != "Resource ID" {
		t.Fatalf("column label = %q", got)
	}
	format := merged[0].Output.ColumnFormats["spendMicro"]
	testutil.Require(t, format.Kind == "currency" && format.Currency == "USD" && format.SourceScale == 6 && format.Grouping && format.MinFractionDigits == 2 && format.MaxFractionDigits == 6, "column format = %#v", format)
	testutil.Require(t, merged[0].Output.ColumnAlignments["spendMicro"] == "right" && merged[0].Output.ColumnAlignments["resourceId"] == "left", "column alignments = %#v", merged[0].Output.ColumnAlignments)
	if got := strings.Join(specs[0].Output.DefaultColumns, ","); got != "id,category" {
		t.Fatalf("source default columns = %q", got)
	}

	for _, output := range []overlay.OutputOverride{
		{DefaultColumns: []string{"displayName", "displayName"}},
		{DefaultColumns: []string{"status..phase"}},
		{DefaultColumns: []string{" name"}},
		{ColumnLabels: map[string]string{"unknown": "Unknown"}},
		{ColumnLabels: map[string]string{"resourceId": ""}},
		{ColumnLabels: map[string]string{"resourceId": " Resource ID"}},
		{ColumnLabels: map[string]string{"resourceId": "Resource\tID"}},
		{ColumnLabels: map[string]string{"resourceId": "Resource\nID"}},
		{ColumnFormats: map[string]overlay.ColumnFormatOverride{"unknown": {Kind: "currency", Currency: "USD", MaxFractionDigits: 2}}},
		{ColumnFormats: map[string]overlay.ColumnFormatOverride{"resourceId": {Kind: "number", Currency: "USD", MaxFractionDigits: 2}}},
		{ColumnFormats: map[string]overlay.ColumnFormatOverride{"resourceId": {Kind: "currency", Currency: "usd", MaxFractionDigits: 2}}},
		{ColumnFormats: map[string]overlay.ColumnFormatOverride{"resourceId": {Kind: "currency", Currency: "USD", SourceScale: -1, MaxFractionDigits: 2}}},
		{ColumnFormats: map[string]overlay.ColumnFormatOverride{"resourceId": {Kind: "currency", Currency: "USD", SourceScale: 6, MinFractionDigits: 2, MaxFractionDigits: 5}}},
		{ColumnFormats: map[string]overlay.ColumnFormatOverride{"resourceId": {Kind: "currency", Currency: "USD", MinFractionDigits: 3, MaxFractionDigits: 2}}},
		{ColumnAlignments: map[string]string{"unknown": "right"}},
		{ColumnAlignments: map[string]string{"resourceId": "center"}},
		{ColumnAlignments: map[string]string{"resourceId": ""}},
	} {
		if output.DefaultColumns == nil {
			output.DefaultColumns = []string{"resourceId", "displayName"}
		}
		bad := overlay.Module{Commands: map[string]overlay.Override{"list": {Output: &output}}}
		if err := ValidateOverlayModule(specs, bad); err == nil {
			t.Fatalf("ValidateOverlayModule accepted output %#v", output)
		}
	}

}

func paramDefault(t *testing.T, specs []runtime.CommandSpec, use string, name string) string {
	t.Helper()
	for _, spec := range specs {
		if spec.Use != use {
			continue
		}
		for _, param := range spec.Params {
			if param.Name == name {
				return param.Default
			}
		}
		t.Fatalf("param %s not found on command %s", name, use)
	}
	t.Fatalf("command %s not found", use)
	return ""
}
