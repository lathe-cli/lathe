package normalize_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/spf13/pflag"

	"github.com/lathe-cli/lathe/internal/codegen/normalize"
	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/lathe"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestReservedParameterFlagsPreserveGlobalOptions(t *testing.T) {
	manifest := &config.Manifest{CLI: config.CLIInfo{Name: "demo", HostEnv: "DEMO_HOST"}}
	root := lathe.NewApp(manifest)
	params := []rawir.RawParameter{{Name: "help", In: "query", Type: "string"}}
	root.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		params = append(params, rawir.RawParameter{Name: flag.Name, In: "query", Type: "string"})
	})
	for _, name := range []string{"query-output", "dry-run", "o", "f", "h"} {
		params = append(params, rawir.RawParameter{Name: name, In: "query", Type: "string"})
	}
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{{
		Group: "Checks", OperationID: "check", Method: "GET", Path: "/check", Parameters: params,
	}}})
	for _, param := range specs[0].Params {
		for _, name := range append([]string{param.Flag}, param.Aliases...) {
			if name == "help" || root.PersistentFlags().Lookup(name) != nil {
				t.Fatalf("parameter %q flag --%s shadows a framework flag", param.Name, name)
			}
		}
	}
	if err := render.ValidateOverlayModule(specs, overlay.Module{}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.BuildFlat(root, "api", specs); err != nil {
		t.Fatal(err)
	}
	args := []string{"checks", "check", "--hostname", "example.com", "-o", "json", "--insecure=false", "--debug=false", "--lathe-dry-run"}
	for _, param := range specs[0].Params {
		args = append(args, "--"+param.Flag, "wire-"+param.Name)
	}
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, &output)
	}
	var preview struct {
		URL      string `json:"url"`
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Hostname != "example.com" {
		t.Fatalf("global hostname = %q", preview.Hostname)
	}
	requestURL, err := url.Parse(preview.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, param := range params {
		if got := requestURL.Query().Get(param.Name); got != "wire-"+param.Name {
			t.Errorf("wire parameter %q = %q", param.Name, got)
		}
	}
}

func TestReservedFlagsOverlayBoundary(t *testing.T) {
	specs := normalize.Normalize(&rawir.RawModule{Operations: []rawir.RawOperation{{
		Group: "Checks", OperationID: "check", Method: "GET", Path: "/check",
		Parameters: []rawir.RawParameter{{Name: "Output", In: "query", Type: "string"}},
	}}})
	for _, flag := range []string{"output", "hostname", "insecure", "debug", "help"} {
		t.Run(flag, func(t *testing.T) {
			mod := overlay.Module{Commands: map[string]overlay.Override{
				"check": {Params: map[string]overlay.ParamOverride{"Output": {Flag: flag}}},
			}}
			if err := render.ValidateOverlayModule(specs, mod); err == nil {
				t.Fatal("overlay restored a reserved parameter flag")
			}
		})
	}
	mod := overlay.Module{Commands: map[string]overlay.Override{
		"check": {
			Params:    map[string]overlay.ParamOverride{"Output": {Flag: "mode"}},
			Shortcuts: []overlay.Shortcut{{Use: "preview", Params: map[string]string{"mode": "diff"}}},
		},
	}}
	if err := render.ValidateOverlayModule(specs, mod); err != nil {
		t.Fatal(err)
	}
	merged, err := render.MergeOverlayModule(specs, mod)
	if err != nil {
		t.Fatal(err)
	}
	root := lathe.NewApp(&config.Manifest{CLI: config.CLIInfo{Name: "demo", HostEnv: "DEMO_HOST"}})
	if err := runtime.BuildFlat(root, "api", merged); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"preview", "--hostname", "example.com", "--dry-run", "-o", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var preview struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.URL != "https://example.com/check?Output=diff" {
		t.Fatalf("overlay shortcut request = %q", preview.URL)
	}
}
