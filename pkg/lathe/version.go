package lathe

import "github.com/spf13/cobra"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func versionInfo() string {
	return Version + " (" + Commit + ", " + Date + ")"
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
