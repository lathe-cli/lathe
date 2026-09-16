package testutil

import (
	"errors"
	"slices"
	"testing"
)

func TestAssertionsReportFailures(t *testing.T) {
	tb := &fakeTB{}
	Require(tb, true, "must not fail")
	Check(tb, true, "must not fail")
	NoError(tb, nil)
	Require(tb, false, "required %d", 1)
	Check(tb, false, "checked %d", 2)
	NoError(tb, errors.New("probe"))
	if !slices.Equal(tb.fatals, []string{"required 1", "unexpected error: probe"}) || !slices.Equal(tb.errors, []string{"checked 2"}) {
		t.Fatalf("fatals=%v errors=%v", tb.fatals, tb.errors)
	}
}
