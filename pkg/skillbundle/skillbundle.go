package skillbundle

import (
	"io/fs"

	kitup "github.com/lathe-cli/kitup/go"
	kitupcobra "github.com/lathe-cli/kitup/go-cobra"
	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

func Mount(root *cobra.Command, fsys fs.FS, dir string) {
	runtime.AttachCapability(root, runtime.CapabilitySkillBundle)
	root.AddCommand(kitupcobra.NewSkillCommand(kitupcobra.Options{
		AppID:  root.Name(),
		Bundle: kitup.FSBundle(fsys, dir),
	}))
}
