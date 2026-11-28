package skillbundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	info := kitup.ValidateSkillBundle(opts.Bundle)
	opts.SkillName = info.SkillName
	cmd := kitupcobra.NewSkillCommand(opts)
	oldStatus, _, _ := cmd.Find([]string{"status"})
	cmd.RemoveCommand(oldStatus)
	var captured bytes.Buffer
	statusOpts := opts
	statusOpts.Out = &captured
	status := kitupcobra.NewStatusCommand(statusOpts)
	run := status.RunE
	status.RunE = func(cmd *cobra.Command, args []string) error {
		out := opts.Out
		if out == nil {
			out = cmd.OutOrStdout()
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		var report kitup.StatusReport
		var runErr error
		if !info.Valid {
			report = kitup.StatusReport{Installed: []kitup.InstalledTarget{}, Missing: []kitup.TargetResult{}, Conflicts: []kitup.TargetStatus{}, Errors: []kitup.ReportError{{Reason: info.ErrorCode}}}
			runErr = fmt.Errorf("invalid bundled Skill: %s", info.ErrorCode)
		} else {
			captured.Reset()
			if err := cmd.Flags().Set("json", "true"); err != nil {
				return err
			}
			runErr = run(cmd, args)
			_ = cmd.Flags().Set("json", fmt.Sprint(jsonOutput))
			if captured.Len() == 0 {
				return runErr
			}
			if err := json.Unmarshal(captured.Bytes(), &report); err != nil {
				return err
			}
		}
		if jsonOutput {
			if err := json.NewEncoder(out).Encode(report); err != nil {
				return err
			}
		} else {
			renderSkillStatus(out, report)
		}
		if runErr != nil {
			return runErr
		}
		hash, err := kitup.ComputeBundleContentHash(opts.Bundle)
		if err != nil {
			return err
		}
		scope, _ := cmd.Flags().GetString("scope")
		stderr := opts.Err
		if stderr == nil {
			stderr = cmd.ErrOrStderr()
		}
		for _, target := range report.Installed {
			if target.Metadata.Hash == hash {
				continue
			}
			hosts := target.HostIDs
			if target.HostID != "" {
				hosts = []string{target.HostID}
			}
			fmt.Fprintf(stderr, "Installed Skill differs from this binary's bundle: %s\nPreview a refresh with `%s skill install --scope %s --agent %s --dry-run`. Refresh only after approval.\n", target.TargetDir, opts.AppID, scope, strings.Join(hosts, ","))
		}
		return nil
	}
	cmd.AddCommand(status)
	return cmd
}

func renderSkillStatus(out io.Writer, report kitup.StatusReport) {
	hosts := func(target kitup.TargetResult) string {
		if len(target.HostIDs) > 0 {
			return strings.Join(target.HostIDs, ",")
		}
		return target.HostID
	}
	for _, item := range report.Installed {
		fmt.Fprintf(out, "installed\t%s\t%s\n", hosts(item.TargetResult), item.TargetDir)
	}
	for _, item := range report.Missing {
		fmt.Fprintf(out, "missing\t%s\t%s\n", hosts(item), item.TargetDir)
	}
	for _, item := range report.Conflicts {
		fmt.Fprintf(out, "conflict\t%s\t%s\t%s\n", hosts(item.TargetResult), item.TargetDir, item.Reason)
	}
	for _, item := range report.Errors {
		fmt.Fprintf(out, "error\t%s\n", item.Reason)
	}
}
