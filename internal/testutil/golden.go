package testutil

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
)

var update = flag.Bool("update", false, "update golden files instead of asserting against them")

// TB captures the subset of testing.TB that AssertGolden needs. *testing.T
// satisfies it; tests inside this package use a fake implementation so
// "the assertion failed" can be observed without failing the outer test.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// AssertGolden compares actual bytes to the contents of goldenPath.
// If -update is set or the file does not exist, actual is written to goldenPath.
// Otherwise a byte-for-byte mismatch fails the test with a line-oriented diff.
func AssertGolden(tb TB, goldenPath string, actual []byte) {
	tb.Helper()

	if *update {
		writeGolden(tb, goldenPath, actual)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeGolden(tb, goldenPath, actual)
			return
		}
		tb.Fatalf("read golden %s: %v", goldenPath, err)
		return
	}

	if bytes.Equal(want, actual) {
		return
	}

	tb.Errorf("golden mismatch: %s\n%s", goldenPath, lineDiff(want, actual))
}

func AssertJSONGolden(tb TB, goldenPath string, actual any) {
	tb.Helper()
	data, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		tb.Fatalf("marshal golden json: %v", err)
		return
	}
	data = append(data, '\n')
	AssertGolden(tb, goldenPath, data)
}

func AssertNamedJSONGolden(tb TB, name string, actual any) {
	tb.Helper()
	AssertJSONGolden(tb, filepath.Join("testdata", name+".golden.json"), actual)
}

func AssertRawModuleGolden(tb TB, name string, mod *rawir.RawModule) {
	tb.Helper()
	sort.Slice(mod.Operations, func(i, j int) bool {
		if mod.Operations[i].Path != mod.Operations[j].Path {
			return mod.Operations[i].Path < mod.Operations[j].Path
		}
		return mod.Operations[i].Method < mod.Operations[j].Method
	})
	AssertNamedJSONGolden(tb, name, mod)
}

func writeGolden(tb TB, path string, data []byte) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		tb.Fatalf("write golden %s: %v", path, err)
		return
	}
}

func lineDiff(want, got []byte) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	n := len(wl)
	if len(gl) > n {
		n = len(gl)
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w == g {
			continue
		}
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteByte('\n')
		b.WriteString("+ ")
		b.WriteString(g)
		b.WriteByte('\n')
	}
	return b.String()
}
