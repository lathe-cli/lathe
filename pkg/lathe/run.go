package lathe

import (
	"context"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

// RunOptions configures the standard entrypoint for a generated Lathe CLI.
type RunOptions struct {
	// Manifest is the embedded cli.yaml content.
	Manifest []byte

	// Mount attaches generated module commands to the root command.
	Mount func(*cobra.Command) error

	// Version, Commit, and Date are build-time values shown by the version command.
	Version string
	Commit  string
	Date    string
}

// Run executes a generated Lathe CLI and returns the process exit code.
func Run(opts RunOptions) int {
	return run(opts, os.Args[1:], os.Stdout, os.Stderr)
}

func run(opts RunOptions, args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		// Restore the default so a second interrupt can terminate context-unaware reads.
		stop()
	}()
	format := requestedOutputFormat(args)

	m, err := config.Load(opts.Manifest)
	if err != nil {
		return runtime.FormatError(err, format, stderr)
	}

	setVersionInfo(opts)

	root := NewApp(m)
	root.SetContext(ctx)
	_ = root.PersistentFlags().Set("output", format)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if opts.Mount != nil {
		if err := opts.Mount(root); err != nil {
			return runtime.FormatError(err, format, stderr)
		}
	}

	return runtime.Execute(root)
}

func requestedOutputFormat(args []string) string {
	flags := pflag.NewFlagSet("output", pflag.ContinueOnError)
	flags.ParseErrorsAllowlist.UnknownFlags = true
	flags.BoolP("help", "h", false, "")
	format := flags.StringP("output", "o", "", "")
	if err := flags.Parse(args); err == nil && flags.Changed("output") {
		return *format
	}
	return "table"
}

func setVersionInfo(opts RunOptions) {
	if opts.Version != "" {
		Version = opts.Version
	}
	if opts.Commit != "" {
		Commit = opts.Commit
	}
	if opts.Date != "" {
		Date = opts.Date
	}
}
