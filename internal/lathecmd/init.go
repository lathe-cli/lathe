package lathecmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/lathe-cli/lathe/internal/projectinit"
	"github.com/lathe-cli/lathe/pkg/lathe"
	"golang.org/x/term"
)

func RunInit(args []string, stdout, stderr io.Writer) error {
	interactive := false
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		if file, ok := stderr.(*os.File); ok {
			interactive = term.IsTerminal(int(file.Fd()))
		}
	}
	return runInit(args, os.Stdin, interactive, stdout, stderr)
}

func runInit(args []string, input io.Reader, interactive bool, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("lathe init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	language := fs.String("language", "", "starter language: node, go, python, or rust")
	template := fs.String("template", "", "template Git URL, optionally followed by #<ref>")
	appName := fs.String("app-name", "", "application display name")
	cliName := fs.String("cli-name", "", "generated CLI name")
	goModule := fs.String("go-module", "", "Go module path for the generated CLI")
	licenseName := fs.String("license", "", "license: mit or none")
	licenseHolder := fs.String("license-holder", "", "MIT license holder")
	jsonOutput := fs.Bool("json", false, "print machine-readable result")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: lathe init <directory> [flags]")
		fs.PrintDefaults()
	}
	target := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if target == "" && fs.NArg() == 1 {
		target = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return errors.New("lathe init requires exactly one target directory")
	}
	reader := bufio.NewReader(input)
	var err error
	if target == "" {
		if !interactive {
			return errors.New("lathe init requires exactly one target directory")
		}
		target, err = prompt(reader, stderr, "Target directory", "my-app")
		if err != nil {
			return err
		}
	}
	slug := filepath.Base(filepath.Clean(target))
	if *language == "" {
		if !interactive {
			return errors.New("--language is required outside an interactive terminal")
		}
		*language, err = selectLanguage(reader, stderr)
		if err != nil {
			return err
		}
	}
	moduleVersion := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = info.Main.Version
	}
	latheVersion, err := resolveInitVersion(lathe.Version, moduleVersion, os.Getenv("LATHE_INIT_VERSION"))
	if err != nil {
		return err
	}
	if interactive {
		if *appName == "" {
			*appName, err = prompt(reader, stderr, "Application name", slug)
			if err != nil {
				return err
			}
		}
		if *cliName == "" {
			*cliName, err = prompt(reader, stderr, "CLI name", slug+"ctl")
			if err != nil {
				return err
			}
		}
		if *goModule == "" {
			*goModule, err = prompt(reader, stderr, "Go module", "example.com/"+slug)
			if err != nil {
				return err
			}
		}
		if *licenseName == "" {
			*licenseName, err = prompt(reader, stderr, "License", "mit")
			if err != nil {
				return err
			}
		}
		if *licenseName == "mit" && *licenseHolder == "" {
			*licenseHolder, err = prompt(reader, stderr, "License holder", *appName+" contributors")
			if err != nil {
				return err
			}
		}
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve lathe executable: %w", err)
	}
	result, err := projectinit.Init(projectinit.Options{
		Target:        target,
		Language:      *language,
		Template:      *template,
		AppName:       *appName,
		CLIName:       *cliName,
		GoModule:      *goModule,
		License:       *licenseName,
		LicenseHolder: *licenseHolder,
		LatheVersion:  latheVersion,
		Stderr:        stderr,
		Bootstrap: func(projectRoot string, output io.Writer) error {
			cmd := exec.Command(executable, "bootstrap")
			cmd.Dir = projectRoot
			cmd.Stdout = output
			cmd.Stderr = output
			return cmd.Run()
		},
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(data))
		return err
	}
	fmt.Fprintf(stdout, "Created %s\n", result.Path)
	fmt.Fprintf(stdout, "Template: %s#%s (%s)\n", result.Template.Repo, result.Template.Ref, result.Template.Commit)
	fmt.Fprintf(stdout, "Next: cd %s && %s\n", result.Path, result.NextCommand)
	return nil
}

func prompt(reader *bufio.Reader, output io.Writer, label, defaultValue string) (string, error) {
	fmt.Fprintf(output, "%s [%s]: ", label, defaultValue)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func selectLanguage(reader *bufio.Reader, output io.Writer) (string, error) {
	fmt.Fprint(output, "Select language:\n  1) Node.js\n  2) Go\n  3) Python\n  4) Rust\n")
	for {
		value, err := prompt(reader, output, "Language", "1")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(value) {
		case "1", "node", "node.js":
			return "node", nil
		case "2", "go":
			return "go", nil
		case "3", "python":
			return "python", nil
		case "4", "rust":
			return "rust", nil
		default:
			fmt.Fprintf(output, "Invalid language selection %q; choose 1-4.\n", value)
		}
	}
}

func resolveInitVersion(linkedVersion, moduleVersion, override string) (string, error) {
	if linkedVersion != "dev" {
		return linkedVersion, nil
	}
	if override != "" {
		return override, nil
	}
	moduleVersion = strings.TrimSuffix(moduleVersion, "+dirty")
	if moduleVersion != "" && moduleVersion != "(devel)" {
		return moduleVersion, nil
	}
	return "", errors.New("cannot determine generator/runtime version; set LATHE_INIT_VERSION")
}
