package latheskill

import (
	"testing"

	kitup "github.com/lathe-cli/kitup/go"
)

func TestBundleIsValid(t *testing.T) {
	info := kitup.ValidateSkillBundle(kitup.FSBundle(FS, Root))
	if !info.Valid || info.SkillName != "lathe" {
		t.Fatalf("skill info = %#v", info)
	}
}
