package skillbundle

import (
	"fmt"
	"io/fs"
	"strings"

	kitup "github.com/lathe-cli/kitup/go"
	kitupcobra "github.com/lathe-cli/kitup/go-cobra"
	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

func Mount(root *cobra.Command, fsys fs.FS, dir string) {
	runtime.AttachCapability(root, runtime.CapabilitySkillBundle)
	root.AddCommand(newSkillCommand(kitupcobra.Options{
		AppID:  root.Name(),
		Bundle: kitup.FSBundle(fsys, dir),
	}))
}

func newSkillCommand(opts kitupcobra.Options) *cobra.Command {
	opts.SkillName = kitup.ValidateSkillBundle(opts.Bundle).SkillName
	cmd := kitupcobra.NewSkillCommand(opts)
	status, _, _ := cmd.Find([]string{"status"})
	run := status.RunE
	status.RunE = func(cmd *cobra.Command, args []string) error {
		if err := run(cmd, args); err != nil {
			return err
		}
		scope, _ := cmd.Flags().GetString("scope")
		agents, _ := cmd.Flags().GetStringArray("agent")
		parsed := kitup.ParseInstallFlags(kitup.InstallFlagValues{Scope: scope, ScopeSet: true, Agents: agents})
		hash, err := kitup.ComputeBundleContentHash(opts.Bundle)
		if err != nil {
			return err
		}
		report, err := kitup.StatusBundledSkill(kitup.StatusOptions{
			BaseOptions: kitup.BaseOptions{Home: opts.Home, CWD: opts.CWD, HostsFile: opts.HostsFile},
			AppID:       opts.AppID,
			SkillName:   opts.SkillName,
			Scope:       parsed.Scope,
			Agents:      parsed.Agents,
		})
		if err != nil {
			return err
		}
		if len(report.Errors) > 0 {
			return fmt.Errorf("check bundled Skill updates: %s", report.Errors[0].Reason)
		}
		for _, target := range report.Installed {
			if target.Metadata.Hash == hash {
				continue
			}
			hosts := target.HostIDs
			if target.HostID != "" {
				hosts = []string{target.HostID}
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Installed Skill differs from this binary's bundle: %s\nPreview a refresh with `%s skill install --scope %s --agent %s --dry-run`. Refresh only after approval.\n", target.TargetDir, opts.AppID, scope, strings.Join(hosts, ","))
		}
		return nil
	}
	return cmd
}
