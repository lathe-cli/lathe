package normalize

import (
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestValidateParameters(t *testing.T) {
	explodeTrue := true
	cases := []struct {
		name  string
		param rawir.RawParameter
	}{
		{name: "object path", param: rawir.RawParameter{Name: "id", In: "path", Type: "object", Style: "simple"}},
		{name: "object query", param: rawir.RawParameter{Name: "filter", In: "query", Type: "object", Style: "form"}},
		{name: "object header", param: rawir.RawParameter{Name: "X-Tenant", In: "header", Type: "object", Style: "simple"}},
		{name: "object cookie", param: rawir.RawParameter{Name: "session", In: "cookie", Type: "object", Style: "form"}},
		{name: "path form", param: rawir.RawParameter{Name: "id", In: "path", Type: "string", Style: "form"}},
		{name: "path deepObject", param: rawir.RawParameter{Name: "id", In: "path", Type: "string", Style: "deepObject"}},
		{name: "query deepObject", param: rawir.RawParameter{Name: "filter", In: "query", Type: "string", Style: "deepObject", Explode: &explodeTrue}},
		{name: "query simple", param: rawir.RawParameter{Name: "q", In: "query", Type: "string", Style: "simple"}},
		{name: "query label", param: rawir.RawParameter{Name: "q", In: "query", Type: "string", Style: "label"}},
		{name: "query matrix", param: rawir.RawParameter{Name: "q", In: "query", Type: "string", Style: "matrix"}},
		{name: "query tabDelimited", param: rawir.RawParameter{Name: "q", In: "query", Type: "array", Style: "tabDelimited"}},
		{name: "header form", param: rawir.RawParameter{Name: "X-A", In: "header", Type: "string", Style: "form"}},
		{name: "cookie simple", param: rawir.RawParameter{Name: "session", In: "cookie", Type: "string", Style: "simple"}},
		{name: "spaceDelimited scalar", param: rawir.RawParameter{Name: "q", In: "query", Type: "string", Style: "spaceDelimited"}},
		{name: "pipeDelimited scalar", param: rawir.RawParameter{Name: "q", In: "query", Type: "string", Style: "pipeDelimited"}},
		{name: "array cookie", param: rawir.RawParameter{Name: "session", In: "cookie", Type: "array", Style: "form"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := ValidateParameters(moduleWithParam(tc.param))
			testutil.Require(t, len(problems) == 1, "problems = %d, want 1 (%v)", len(problems), problems)
			msg := problems[0].Error()
			for _, want := range []string{tc.param.Name, tc.param.In, tc.param.Style, "GET /items (Items_List)", "fix the specification or exclude the operation"} {
				testutil.Check(t, strings.Contains(msg, want), "error %q missing %q", msg, want)
			}
		})
	}
}

func TestValidateParameters_CollectsEveryProblem(t *testing.T) {
	mod := &rawir.RawModule{Operations: []rawir.RawOperation{{
		OperationID: "Items_List",
		Method:      "GET",
		Path:        "/items",
		Parameters: []rawir.RawParameter{
			{Name: "filter", In: "query", Type: "object", Style: "deepObject"},
			{Name: "session", In: "cookie", Type: "array"},
		},
	}}}
	problems := ValidateParameters(mod)
	testutil.Require(t, len(problems) == 2, "problems = %d, want 2 (%v)", len(problems), problems)
	testutil.Check(t, problems[0].Name == "filter" && problems[0].Style == "deepObject", "first = %+v", problems[0])
	testutil.Check(t, problems[1].Name == "session" && problems[1].Style == "form", "second = %+v", problems[1])
}

func TestValidateParameters_AllowsReservedOutsideQuery(t *testing.T) {
	problems := ValidateParameters(moduleWithParam(rawir.RawParameter{Name: "id", In: "path", Type: "string", AllowReserved: true}))
	testutil.Require(t, len(problems) == 0, "problems = %v", problems)
	spec := Normalize(moduleWithParam(rawir.RawParameter{Name: "id", In: "path", Type: "string", AllowReserved: true}))[0]
	testutil.Check(t, !spec.Params[0].AllowReserved && spec.Params[0].Style == "", "param = %+v", spec.Params[0])
}

func TestNormalize_DisambiguatesCookieFlags(t *testing.T) {
	cases := []struct {
		name   string
		params []rawir.RawParameter
		want   map[string]string
	}{
		{
			name: "cookie yields to query and header",
			params: []rawir.RawParameter{
				{Name: "tenant", In: "query", Type: "string"},
				{Name: "tenant", In: "header", Type: "string"},
				{Name: "tenant", In: "cookie", Type: "string"},
				{Name: "color", In: "header", Type: "string"},
				{Name: "color", In: "cookie", Type: "string"},
			},
			want: map[string]string{
				"query tenant":  "tenant",
				"header tenant": "tenant",
				"cookie tenant": "cookie-tenant",
				"header color":  "color",
				"cookie color":  "cookie-color",
			},
		},
		{
			name: "suffix when cookie prefix is taken",
			params: []rawir.RawParameter{
				{Name: "tenant", In: "query", Type: "string"},
				{Name: "cookie-tenant", In: "query", Type: "string"},
				{Name: "tenant", In: "cookie", Type: "string"},
				{Name: "tenant", In: "cookie", Type: "string"},
			},
			want: map[string]string{
				"query tenant":        "tenant",
				"query cookie-tenant": "cookie-tenant",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specs := Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{{
				OperationID: "Items_List",
				Method:      "GET",
				Path:        "/items",
				Parameters:  tc.params,
			}}})
			testutil.Require(t, len(specs) == 1, "specs = %d", len(specs))
			got := map[string]string{}
			for _, param := range specs[0].Params {
				key := param.In + " " + param.Name
				if _, exists := got[key]; exists {
					got[key+" "+param.Flag] = param.Flag
				} else {
					got[key] = param.Flag
				}
			}
			if tc.name == "suffix when cookie prefix is taken" {
				flags := map[string]bool{}
				for _, param := range specs[0].Params {
					testutil.Check(t, !flags[param.Flag], "duplicate flag %q", param.Flag)
					flags[param.Flag] = true
				}
				testutil.Check(t, flags["cookie-tenant-2"] && flags["cookie-tenant-3"], "flags = %v", flags)
			}
			for key, flag := range tc.want {
				testutil.Check(t, got[key] == flag, "%s flag = %q, want %q", key, got[key], flag)
			}
		})
	}
}

func TestValidateParameters_UsesCommandPath(t *testing.T) {
	mod := moduleWithParam(rawir.RawParameter{Name: "filter", In: "query", Type: "object", Style: "deepObject"})
	mod.Operations[0].ServerBasePath = "/api"
	problems := ValidateParameters(mod)
	testutil.Require(t, len(problems) == 1 && problems[0].Path == "/api/items" && problems[0].Method == "GET" && problems[0].OperationID == "Items_List", "problem = %+v", problems[0])
}

func TestNormalize_InvalidParameterUsesDefaultSerialization(t *testing.T) {
	specs := Normalize(moduleWithParam(rawir.RawParameter{Name: "filter", In: "query", Type: "object", Style: "deepObject"}))
	testutil.Require(t, len(specs) == 1 && len(specs[0].Params) == 1, "specs = %+v", specs)
	testutil.Check(t, specs[0].Params[0].Style == "" && !specs[0].Params[0].Explode && specs[0].Params[0].GoType == "string", "param = %+v", specs[0].Params[0])
}

func moduleWithParam(p rawir.RawParameter) *rawir.RawModule {
	return &rawir.RawModule{Operations: []rawir.RawOperation{{
		OperationID: "Items_List",
		Method:      "GET",
		Path:        "/items",
		Parameters:  []rawir.RawParameter{p},
		Responses:   map[string]*rawir.RawResponse{},
	}}}
}

func TestNormalize_DropsDefaultSerialization(t *testing.T) {
	explodeTrue := true
	specs := Normalize(moduleWithParam(rawir.RawParameter{Name: "q", In: "query", Type: "array", Style: "form", Explode: &explodeTrue}))
	param := specs[0].Params[0]
	testutil.Check(t, param.GoType == "[]string" && param.Style == "" && !param.Explode, "param = %+v", param)
}
