package lathe

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

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
	format := errorOutputFormat(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	m, err := config.Load(opts.Manifest)
	if err != nil {
		if ctx.Err() != nil {
			return runtime.FormatError(ctx.Err(), format, stderr)
		}
		err = runtime.NewError(runtime.CodeGeneral, runtime.ExitGeneral, "invalid CLI configuration", "fix cli.yaml and retry", err)
		return runtime.FormatError(err, format, stderr)
	}

	setVersionInfo(opts)

	root := NewApp(m)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if format == "json" || format == "yaml" {
		_ = root.PersistentFlags().Set("output", format)
	}
	root.SetContext(ctx)

	if opts.Mount != nil {
		if err := opts.Mount(root); err != nil {
			if ctx.Err() != nil {
				return runtime.FormatError(ctx.Err(), format, stderr)
			}
			err = runtime.NewError(runtime.CodeGeneral, runtime.ExitGeneral, "generated CLI failed to start", "re-run code generation and rebuild the CLI", err)
			return runtime.FormatError(err, format, stderr)
		}
	}

	return runtime.Execute(root)
}

func errorOutputFormat(args []string) string {
	format := "table"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		var value string
		switch {
		case arg == "--output" || arg == "-o":
			if i+1 < len(args) {
				value = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--output="):
			value = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			value = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "-o") && len(arg) > 2:
			value = arg[2:]
		}
		if value != "" {
			format = "table"
			if value == "json" || value == "yaml" {
				format = value
			}
		}
	}
	return format
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
