package latheskill

import (
	"testing"

	kitup "github.com/lathe-cli/kitup/go"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestBundleIsValid(t *testing.T) {
	info := kitup.ValidateSkillBundle(kitup.FSBundle(FS, Root))
	testutil.Require(t, info.Valid && info.SkillName == "lathe", "skill info = %#v", info)
}
