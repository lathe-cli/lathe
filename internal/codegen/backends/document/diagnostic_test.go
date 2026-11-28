package document

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMergeNamedDiagnosticsFollowKeyOrder(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	prior := os.Stderr
	os.Stderr = out
	defer func() { os.Stderr = prior }()
	dst, add := map[string]int{}, map[string]int{}
	for i := 0; i < 32; i++ {
		key := fmt.Sprintf("key-%02d", i)
		dst[key] = 1
		add[key] = 2
	}
	MergeNamed(dst, add, "schema", "api", "b.json")
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 32 {
		t.Fatalf("warnings = %d", len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(line, fmt.Sprintf("key-%02d", i)) {
			t.Fatalf("warning %d = %q", i, line)
		}
	}
}
