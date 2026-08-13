package lathe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

type verifyReport struct {
	Version int           `json:"version"`
	OK      bool          `json:"ok"`
	Checks  []verifyCheck `json:"checks"`
}

type verifyCheck struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type verifyFailedError struct{}

const verifyReportVersion = 1

func (verifyFailedError) Error() string {
	return "generated CLI verify failed"
}

func (verifyFailedError) SilentExitCode() int {
	return runtime.ExitGeneral
}

func verifyCmd(m *config.Manifest) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify generated CLI contract",
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := verifyGenerated(cmd.Root(), m)
			if err := writeJSON(cmd, report); err != nil {
				return err
			}
			if !report.OK {
				return verifyFailedError{}
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Emit verify JSON")
	return cmd
}

func verifyGenerated(root *cobra.Command, m *config.Manifest) verifyReport {
	report := verifyReport{Version: verifyReportVersion, OK: true}
	catalog := runtime.BuildCatalog(root, catalogOptions(m, false))

	report.add("root_help", verifyRootHelp(root, m.CLI.Name))
	report.add("commands_schema", verifyCommandsSchema(root, catalog))
	report.add("commands_json", verifyCommandsJSON(catalog))
	report.add("catalog_nonempty", verifyCatalogNonempty(catalog))
	for _, entry := range catalog.Commands {
		report.add("commands_show:"+strings.Join(entry.Path, " "), verifyCatalogEntry(root, m, entry))
	}
	report.add("auth_status_unauthenticated", verifyAuthStatusUnauthenticated(m))
	if runtime.HasCapability(root, runtime.CapabilitySkillBundle) {
		report.add("skill_install", verifySkillInstall(root, m))
	}
	if runtime.HasCapability(root, runtime.CapabilityWorkflowDSL) {
		report.add("workflow_contract", verifyWorkflowContract(runtime.BuildCatalog(root, catalogOptions(m, true))))
	}

	return report
}

func (r *verifyReport) add(name string, err error) {
	check := verifyCheck{Name: name, OK: err == nil}
	if err != nil {
		check.Error = err.Error()
		r.OK = false
	}
	r.Checks = append(r.Checks, check)
}

func verifyRootHelp(root *cobra.Command, cliName string) error {
	if root == nil {
		return errors.New("root command is nil")
	}
	if root.Use != cliName {
		return fmt.Errorf("root use = %q, want %q", root.Use, cliName)
	}
	if findCommand(root, []string{"commands"}) == nil {
		return errors.New("missing commands command")
	}
	if findCommand(root, []string{"search"}) == nil {
		return errors.New("missing search command")
	}
	for _, want := range []string{"commands --json", "commands show", "search"} {
		if !strings.Contains(root.Long, want) {
			return fmt.Errorf("root help missing %q", want)
		}
	}
	if root.UsageString() == "" {
		return errors.New("root usage is empty")
	}
	return nil
}

func verifyCommandsSchema(root *cobra.Command, catalog runtime.Catalog) error {
	if findCommand(root, []string{"commands", "schema"}) == nil {
		return errors.New("missing commands schema command")
	}
	if catalog.CatalogSchemaVersion != runtime.CatalogSchemaVersion {
		return fmt.Errorf("catalog schema = %d, want %d", catalog.CatalogSchemaVersion, runtime.CatalogSchemaVersion)
	}
	return nil
}

func verifyCommandsJSON(catalog runtime.Catalog) error {
	_, err := json.Marshal(catalog)
	return err
}

func verifyCatalogNonempty(catalog runtime.Catalog) error {
	if len(catalog.Commands) == 0 {
		return errors.New("visible generated command catalog is empty")
	}
	return nil
}

func verifyCatalogEntry(root *cobra.Command, m *config.Manifest, entry runtime.CatalogCommand) error {
	path := strings.Join(entry.Path, " ")
	if len(entry.Path) == 0 {
		return errors.New("catalog entry path is empty")
	}
	cmd := findCommand(root, entry.Path)
	if cmd == nil {
		return fmt.Errorf("cobra command not found for path %q", path)
	}
	found, ok := runtime.FindCatalogCommand(root, entry.Path, catalogOptions(m, false))
	if !ok {
		return fmt.Errorf("commands show cannot find %q", path)
	}
	if !reflect.DeepEqual(found, entry) {
		return fmt.Errorf("commands show mismatch for %q", path)
	}
	for _, flag := range entry.Flags {
		if flag.Flag == "" {
			return fmt.Errorf("%q has catalog flag with empty name", path)
		}
		if cmd.Flags().Lookup(flag.Flag) == nil {
			return fmt.Errorf("%q catalog requires missing --%s flag", path, flag.Flag)
		}
	}
	if entry.Body != nil && entry.Body.Required {
		switch {
		case isJSONBody(entry.Body.MediaType):
			for _, name := range []string{"file", "set", "set-str"} {
				if cmd.Flags().Lookup(name) == nil {
					return fmt.Errorf("%q required body missing --%s flag", path, name)
				}
			}
		case isMultipartBody(entry.Body.MediaType):
		default:
			if cmd.Flags().Lookup("file") == nil {
				return fmt.Errorf("%q required body missing --file flag", path)
			}
		}
	}
	return nil
}

func verifyWorkflowContract(catalog runtime.Catalog) error {
	count := 0
	for _, entry := range catalog.Commands {
		if entry.Kind != "workflow" {
			continue
		}
		count++
		if entry.Workflow == nil {
			return fmt.Errorf("workflow command %q missing workflow metadata", strings.Join(entry.Path, " "))
		}
		if entry.Workflow.DSL != "lathe.workflow.v1" {
			return fmt.Errorf("workflow command %q DSL = %q", strings.Join(entry.Path, " "), entry.Workflow.DSL)
		}
		if len(entry.Workflow.Steps) == 0 {
			return fmt.Errorf("workflow command %q has no steps", strings.Join(entry.Path, " "))
		}
		for _, step := range entry.Workflow.Steps {
			if step.ID == "" {
				return fmt.Errorf("workflow command %q has a step with empty id", strings.Join(entry.Path, " "))
			}
			if step.HTTP.Method == "" || step.HTTP.PathTemplate == "" {
				return fmt.Errorf("workflow command %q step %q missing operation HTTP metadata", strings.Join(entry.Path, " "), step.ID)
			}
			for i, cond := range step.When {
				if cond.Value == "" {
					return fmt.Errorf("workflow command %q step %q condition %d missing value", strings.Join(entry.Path, " "), step.ID, i)
				}
				if cond.Operator != "in" && cond.Operator != "notin" {
					return fmt.Errorf("workflow command %q step %q condition %d operator = %q", strings.Join(entry.Path, " "), step.ID, i, cond.Operator)
				}
				if len(cond.Values) == 0 {
					return fmt.Errorf("workflow command %q step %q condition %d has no values", strings.Join(entry.Path, " "), step.ID, i)
				}
			}
		}
	}
	if count == 0 {
		return errors.New("workflow capability is present but no workflow commands are cataloged")
	}
	return nil
}

func isJSONBody(mediaType string) bool {
	mt := normalizedMediaType(mediaType)
	return mt == "" || mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func isMultipartBody(mediaType string) bool {
	return normalizedMediaType(mediaType) == "multipart/form-data"
}

func normalizedMediaType(mediaType string) string {
	mt, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mediaType)), ";")
	return strings.TrimSpace(mt)
}

func verifyAuthStatusUnauthenticated(m *config.Manifest) error {
	if m == nil {
		return errors.New("manifest is nil")
	}
	manifest := *m
	if manifest.CLI.ConfigDir == "" {
		manifest.CLI.ConfigDir = manifest.CLI.Name
	}
	if manifest.CLI.ConfigDirEnv == "" {
		manifest.CLI.ConfigDirEnv = strings.ToUpper(manifest.CLI.Name) + "_CONFIG_DIR"
	}
	tempDir, err := os.MkdirTemp("", manifest.CLI.Name+"-verify-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	old, hadOld := os.LookupEnv(manifest.CLI.ConfigDirEnv)
	if err := os.Setenv(manifest.CLI.ConfigDirEnv, tempDir); err != nil {
		return err
	}
	defer func() {
		if hadOld {
			_ = os.Setenv(manifest.CLI.ConfigDirEnv, old)
		} else {
			_ = os.Unsetenv(manifest.CLI.ConfigDirEnv)
		}
		config.Bind(m)
	}()

	root := NewApp(&manifest)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"auth", "status"})
	err = root.Execute()
	if err == nil {
		return errors.New("auth status unexpectedly passed with empty isolated config")
	}
	if runtime.ClassifyError(err).Code != runtime.CodeNotAuthenticated {
		return fmt.Errorf("auth status error = %v, want not authenticated", err)
	}
	return nil
}

func verifySkillInstall(root *cobra.Command, m *config.Manifest) error {
	if findCommand(root, []string{"skill", "install"}) == nil {
		return errors.New("missing skill install command")
	}
	tempHome, err := os.MkdirTemp("", m.CLI.Name+"-skill-verify-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tempHome)
	}()

	oldHome, hadHome := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		return err
	}
	defer func() {
		if hadHome {
			_ = os.Setenv("HOME", oldHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
	}()

	var out bytes.Buffer
	oldOut := root.OutOrStdout()
	oldErr := root.ErrOrStderr()
	root.SetOut(&out)
	root.SetErr(&out)
	defer func() {
		root.SetOut(oldOut)
		root.SetErr(oldErr)
	}()
	root.SetArgs([]string{"skill", "install", "--scope", "user", "--agent", "codex", "--yes"})
	if err := root.Execute(); err != nil {
		return fmt.Errorf("skill install: %w", err)
	}
	target := filepath.Join(tempHome, ".agents", "skills", skillName(m.CLI.Name))
	for _, name := range []string{"SKILL.md", ".kitup.json"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			return fmt.Errorf("skill install missing %s: %w", name, err)
		}
	}
	return nil
}

func skillName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "cli"
	}
	return out
}

func findCommand(root *cobra.Command, path []string) *cobra.Command {
	cur := root
	for _, segment := range path {
		var next *cobra.Command
		for _, child := range cur.Commands() {
			if child.Name() == segment {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
