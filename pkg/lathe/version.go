package lathe

import (
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// VersionInfo reports the values linked at build time, falling back to what Go
// records in the binary. `go install <module>@<ref>` links no ldflags, so
// without the fallback every such build calls itself "dev (none, unknown)".
func VersionInfo() (version, commit, date string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version, Commit, Date
	}
	var revision, vcsTime string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			vcsTime = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return resolveVersion(Version, Commit, Date, info.Main.Version, revision, vcsTime, modified)
}

func resolveVersion(linkedVersion, linkedCommit, linkedDate, moduleVersion, revision, vcsTime string, modified bool) (string, string, string) {
	version, commit, date := linkedVersion, linkedCommit, linkedDate

	if version == "dev" {
		if v := strings.TrimSuffix(moduleVersion, "+dirty"); v != "" && v != "(devel)" {
			version = v
		}
	}
	if commit == "none" && revision != "" {
		commit = revision
		if modified {
			// A dirty tree must not pass for the commit it names.
			commit += "+dirty"
		}
	}
	if date == "unknown" && vcsTime != "" {
		date = vcsTime
	}
	return version, commit, date
}

// catalogCLIVersion resolves the same way the `version` command does, so one
// binary does not answer the question two ways.
func catalogCLIVersion() string {
	version, _, _ := VersionInfo()
	return version
}

func versionInfo() string {
	version, commit, date := VersionInfo()
	return version + " (" + commit + ", " + date + ")"
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("%s %s\n", cmd.Root().Use, versionInfo())
		},
	}
}
