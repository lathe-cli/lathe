package normalize

import (
	"reflect"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestNormalize_ParameterFlagsUseKebabCase(t *testing.T) {
	specs := Normalize(&rawir.RawModule{Name: "demo", Operations: []rawir.RawOperation{{
		Group:       "Apps",
		OperationID: "Apps_Get",
		Method:      "GET",
		Path:        "/apps/{app_id}",
		Parameters: []rawir.RawParameter{
			{Name: "app_id", In: "path", Type: "string", Required: true},
			{Name: "page_size", In: "query", Type: "integer"},
			{Name: "external_id", In: "query", Type: "string"},
			{Name: "external-id", In: "header", Type: "string"},
		},
	}}})

	want := map[string]struct {
		flag    string
		aliases []string
	}{
		"app_id":      {flag: "app-id", aliases: []string{"app_id"}},
		"page_size":   {flag: "page-size", aliases: []string{"page_size"}},
		"external_id": {flag: "external_id"},
		"external-id": {flag: "external-id"},
	}
	for _, param := range specs[0].Params {
		if got := want[param.Name]; param.Flag != got.flag || !reflect.DeepEqual(param.Aliases, got.aliases) {
			t.Errorf("param %q = flag %q aliases %#v, want %q %#v", param.Name, param.Flag, param.Aliases, got.flag, got.aliases)
		}
	}
}

func TestNormalizeIsDeterministicForDuplicateOperationIDs(t *testing.T) {
	opA := rawir.RawOperation{Group: "Svc", OperationID: "Svc_Get", Method: "GET", Path: "/apis/v1alpha2/things/{id}"}
	opB := rawir.RawOperation{Group: "Svc", OperationID: "Svc_Get", Method: "GET", Path: "/apis/v1alpha1/things/{id}"}
	fwd := Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{opA, opB}})
	rev := Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{opB, opA}})
	testutil.Require(t, len(fwd) == 2 && len(rev) == 2, "want 2 specs, got %d and %d", len(fwd), len(rev))
	for j := range fwd {
		testutil.Require(t, fwd[j].PathTpl == rev[j].PathTpl, "output depends on input order: fwd[%d]=%q, rev[%d]=%q", j, fwd[j].PathTpl, j, rev[j].PathTpl)
	}
}

func TestOpNameKeepsVerbWhenPrefixIsNotTheGroup(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Chunk", OperationID: "create_chunk", Method: "POST", Path: "/api/chunk"},
		{Group: "Chunk", OperationID: "update_chunk", Method: "PUT", Path: "/api/chunk"},
		{Group: "Chunk", OperationID: "delete_chunk", Method: "DELETE", Path: "/api/chunk"},
		{Group: "Chunks", OperationID: "Chunks_search", Method: "POST", Path: "/api/chunk/search"},
	}}
	got := map[string]string{}
	for _, spec := range Normalize(mod) {
		got[spec.OperationID] = spec.Use
	}
	want := map[string]string{
		"create_chunk":  "create-chunk",
		"update_chunk":  "update-chunk",
		"delete_chunk":  "delete-chunk",
		"Chunks_search": "search",
	}
	for id, expected := range want {
		testutil.Check(t, got[id] == expected, "%s: want Use %q, got %q", id, expected, got[id])
	}
}

func TestOpNameDropsModulePrefix(t *testing.T) {
	mod := &rawir.RawModule{
		Name: "console",
		Operations: []rawir.RawOperation{{
			Group: "Apps", OperationID: "console_listApps", Method: "POST", Path: "/graphql",
		}},
	}
	specs := Normalize(mod)
	if got := specs[0].Use; got != "list-apps" {
		t.Fatalf("Use = %q, want list-apps", got)
	}
}

func TestOpNameDropsExactRepeatedPrefix(t *testing.T) {
	mod := &rawir.RawModule{
		Name: "skoala",
		Operations: []rawir.RawOperation{
			{Group: "Gateway", OperationID: "GatewayOverview_GatewayOverviewAPIStats", Method: "GET", Path: "/stats/api"},
			{Group: "Gateway", OperationID: "User_UserlandStats", Method: "GET", Path: "/stats/userland"},
		},
	}
	got := map[string]string{}
	for _, spec := range Normalize(mod) {
		got[spec.OperationID] = spec.Use
	}
	want := map[string]string{
		"GatewayOverview_GatewayOverviewAPIStats": "gateway-overview-api-stats",
		"User_UserlandStats":                      "user-userland-stats",
	}
	testutil.Require(t, reflect.DeepEqual(got, want), "uses = %#v, want %#v", got, want)
}

func TestSynthUseNameDropsSharedNoisePrefix(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Dashboards", Method: "GET", Path: "/api/v1/dashboard/"},
		{Group: "Dashboards", Method: "DELETE", Path: "/api/v1/dashboard/{pk}/favorites/"},
		{Group: "Charts", Method: "POST", Path: "/api/v1/advanced_data_type/convert"},
	}}
	want := map[string]string{
		"GET":    "get-dashboard",
		"DELETE": "delete-dashboard-pk-favorites",
		"POST":   "post-advanced-data-type-convert",
	}
	for _, spec := range Normalize(mod) {
		testutil.Check(t, want[spec.Method] == spec.Use, "%s %s: want Use %q, got %q", spec.Method, spec.PathTpl, want[spec.Method], spec.Use)
	}
}

func TestSynthUseNameKeepsDivergingVersions(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{
		{Group: "Users", Method: "GET", Path: "/api/v1/users"},
		{Group: "Users", Method: "GET", Path: "/api/v2/users"},
	}}
	specs := Normalize(mod)
	uses := []string{specs[0].Use, specs[1].Use}
	for _, use := range uses {
		testutil.Check(t, use == "get-v1-users" || use == "get-v2-users", "want versioned command names, got %v", uses)
	}
	testutil.Check(t, uses[0] != uses[1], "versions collapsed onto one name: %v", uses)
}

func TestKebabFromIDTrimsEdgeSeparators(t *testing.T) {
	cases := map[string]string{"_foo": "foo", "foo_": "foo", "a__b": "a-b", "_": ""}
	for id, want := range cases {
		if got := kebabFromID(id); got != want {
			t.Errorf("kebabFromID(%q) = %q, want %q", id, got, want)
		}
	}
}
