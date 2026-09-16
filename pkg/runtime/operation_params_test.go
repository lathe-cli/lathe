package runtime

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBuild_VariableFlagsMergeIntoEnvelope(t *testing.T) {
	root, url, recorded := newRecordingRoot(t, createAppSpec())
	root.SetArgs([]string{"--hostname", url, "demo", "apps", "create-app", "--input-name", "demo"})
	testutil.NoError(t, root.Execute())

	rawBody, _ := recorded()
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(rawBody, &got))
	if q, _ := got["query"].(string); !strings.Contains(q, "mutation createApp") {
		t.Errorf("query missing baked document: %#v", got["query"])
	}
	vars, _ := got["variables"].(map[string]any)
	input, _ := vars["input"].(map[string]any)
	testutil.Check(t, input["name"] == "demo", "variables = %#v, want input.name=demo", got["variables"])
}

func TestBuild_SensitiveVariableSafeInputModes(t *testing.T) {
	root, url, recorded := newRecordingRoot(t, createCredentialSpec())
	t.Setenv("OPENAI_API_KEY", "sk-env")

	cmd := mustFindChild(t, mustFindChild(t, mustFindChild(t, root, "demo"), "credentials"), "create-credential")
	for _, flag := range []string{"input-api-key-env", "input-api-key-file", "input-api-key-stdin"} {
		testutil.Require(t, cmd.Flag(flag) != nil, "missing --%s", flag)
	}

	root.SetArgs([]string{"--hostname", url, "demo", "credentials", "create-credential", "--input_api_key-env", "OPENAI_API_KEY"})
	testutil.NoError(t, root.Execute())
	body, called := recorded()
	testutil.Require(t, called, "request was not sent")
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(body, &got))
	vars, _ := got["variables"].(map[string]any)
	input, _ := vars["input"].(map[string]any)
	testutil.Require(t, input["apiKey"] == "sk-env", "apiKey = %#v, want sk-env", input["apiKey"])
}

func TestBuild_RequiredVariableCanComeFromBodyInput(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		fileBody string
		wantName string
		wantErr  bool
	}{
		{
			name:     "file",
			args:     []string{"--file", "BODY_FILE"},
			fileBody: `{"input":{"name":"from-file"}}`,
			wantName: "from-file",
		},
		{
			name:     "set",
			args:     []string{"--set", "input.name=from-set"},
			wantName: "from-set",
		},
		{
			name:    "missing",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, url, recorded := newRecordingRoot(t, createAppSpec())
			args := append([]string{"--hostname", url, "demo", "apps", "create-app"}, tc.args...)
			if tc.fileBody != "" {
				bodyFile := t.TempDir() + "/body.json"
				testutil.NoError(t, os.WriteFile(bodyFile, []byte(tc.fileBody), 0600))
				for i := range args {
					if args[i] == "BODY_FILE" {
						args[i] = bodyFile
					}
				}
			}
			root.SetArgs(args)
			err := root.Execute()
			if tc.wantErr {
				testutil.Require(t, err != nil, "expected error")
				_, called := recorded()
				testutil.Require(t, !called, "request should not be sent")
				return
			}
			testutil.Require(t, err == nil, "Execute: %v", err)
			rawBody, called := recorded()
			testutil.Require(t, called, "request was not sent")
			var got map[string]any
			testutil.NoError(t, json.Unmarshal(rawBody, &got))
			vars, _ := got["variables"].(map[string]any)
			input, _ := vars["input"].(map[string]any)
			testutil.Check(t, input["name"] == tc.wantName, "variables = %#v, want input.name=%s", got["variables"], tc.wantName)
		})
	}
}

func createCredentialSpec() CommandSpec {
	return CommandSpec{
		Group:   "Credentials",
		Use:     "create-credential",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "input.apiKey", Flag: "input-api-key", Aliases: []string{"input_api_key"}, In: InVariable, GoType: "string", Required: true, Help: "API key"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation createCredential($input: CredentialInput!) { createCredential(input: $input) { id } }","variables":{"input":{}}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}
}

func createAppSpec() CommandSpec {
	return CommandSpec{
		Group:   "Apps",
		Use:     "create-app",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "input.name", Flag: "input-name", In: InVariable, GoType: "string", Required: true, Help: "name"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation createApp($input: CreateAppInput!) { createApp(input: $input) { id } }","variables":{"input":{}}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}
}

func TestBuild_FloatVariableSentAsJSONNumber(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Apps",
		Use:     "set-weight",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "weight", Flag: "weight", In: InVariable, GoType: "float64", Required: true, Help: "weight"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation setWeight($weight: Float!) { setWeight(weight: $weight) { id } }","variables":{}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}}

	root, url, recorded := newRecordingRoot(t, specs[0])
	root.SetArgs([]string{"--hostname", url, "demo", "apps", "set-weight", "--weight", "1.5"})
	testutil.NoError(t, root.Execute())

	rawBody, _ := recorded()
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(rawBody, &got))
	vars, _ := got["variables"].(map[string]any)
	testutil.Check(t, vars["weight"] == 1.5, "variables.weight = %#v (%T), want 1.5 (float64)", vars["weight"], vars["weight"])
}

func TestBuild_IntListVariableSentAsJSONNumbers(t *testing.T) {
	specs := []CommandSpec{{
		Group:   "Apps",
		Use:     "set-ids",
		Method:  "POST",
		PathTpl: "/graphql",
		Params: []ParamSpec{
			{Name: "ids", Flag: "ids", In: InVariable, GoType: "[]int64", Required: true, Help: "ids"},
		},
		RequestBody: &RequestBody{
			Required:  true,
			MediaType: "application/json",
			Template:  `{"query":"mutation setIds($ids: [Int!]!) { setIds(ids: $ids) { id } }","variables":{}}`,
			MergePath: "variables",
		},
		Security: &SecurityHint{Public: true},
	}}

	root, url, recorded := newRecordingRoot(t, specs[0])
	root.SetArgs([]string{"--hostname", url, "demo", "apps", "set-ids", "--ids", "1", "--ids", "2"})
	testutil.NoError(t, root.Execute())

	rawBody, _ := recorded()
	var got map[string]any
	testutil.NoError(t, json.Unmarshal(rawBody, &got))
	vars, _ := got["variables"].(map[string]any)
	ids, ok := vars["ids"].([]any)
	testutil.Check(t, ok && len(ids) == 2 && ids[0] == float64(1) && ids[1] == float64(2), "variables.ids = %#v, want [1,2] as JSON numbers", vars["ids"])
}
