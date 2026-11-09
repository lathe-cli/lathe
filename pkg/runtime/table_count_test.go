package runtime

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestTableArrayCountContract(t *testing.T) {
	data := []byte(`{"items":[{"id":"zero","meta":{"deployments":[]}},{"id":"one","meta":{"deployments":[{}]}},{"id":"two","meta":{"deployments":[{},{}]}},{"id":"missing"},{"id":"null","meta":{"deployments":null}},{"id":"scalar","meta":{"deployments":5}},{"id":"object","meta":{"deployments":{}}}]}`)
	hints := OutputHints{ListPath: "items", DefaultColumns: []string{"id", "meta.deployments"}}
	var plain, counted bytes.Buffer
	testutil.NoError(t, FormatOutput(data, "table", &plain, hints))
	testutil.Require(t, plain.String() == "ID       DEPLOYMENTS\nzero     \none      \ntwo      \nmissing  \nnull     \nscalar   5\nobject   \n", "unconfigured table = %q", plain.String())
	hints.ColumnFormats = map[string]ColumnFormat{"meta.deployments": {Kind: "count"}}
	testutil.NoError(t, FormatOutput(data, "table", &counted, hints))
	lines := strings.Split(strings.TrimSuffix(counted.String(), "\n"), "\n")
	want := []string{"ID DEPLOYMENTS", "zero 0", "one 1", "two 2", "missing", "null", "scalar", "object"}
	testutil.Require(t, len(lines) == len(want), "counted table = %q", counted.String())
	for i, line := range lines {
		testutil.Check(t, strings.Join(strings.Fields(line), " ") == want[i], "row %d = %q, want %q", i, line, want[i])
	}
	for _, output := range []string{"json", "yaml", "raw"} {
		var before, after bytes.Buffer
		testutil.NoError(t, FormatOutput(data, output, &after, hints))
		hints.ColumnFormats = nil
		testutil.NoError(t, FormatOutput(data, output, &before, hints))
		hints.ColumnFormats = map[string]ColumnFormat{"meta.deployments": {Kind: "count"}}
		testutil.Require(t, bytes.Equal(before.Bytes(), after.Bytes()), "%s output changed", output)
	}
}

func TestTableCountOnlyColumnSelection(t *testing.T) {
	for _, data := range []string{`{"items":[{"id":"zero","deployments":[]}]}`, `{"items":[{"id":"one","deployments":[{}]}]}`} {
		var out bytes.Buffer
		testutil.NoError(t, FormatOutput([]byte(data), "table", &out, OutputHints{
			ListPath: "items", DefaultColumns: []string{"deployments"},
			ColumnFormats: map[string]ColumnFormat{"deployments": {Kind: "count"}},
		}))
		testutil.Require(t, strings.HasPrefix(out.String(), "DEPLOYMENTS\n"), "count column was replaced by fallback: %q", out.String())
	}
}
