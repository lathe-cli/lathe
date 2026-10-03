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
	Version    int              `json:"version"`
	OK         bool             `json:"ok"`
	Provenance verifyProvenance `json:"provenance"`
	Checks     []verifyCheck    `json:"checks"`
}

type verifyProvenance struct {
	CLI                  verifyCLI                  `json:"cli"`
	SchemaVersion        int                        `json:"schema_version"`
	CatalogSchemaVersion int                        `json:"catalog_schema_version"`
	Sources              []runtime.SourceProvenance `json:"sources"`
}

type verifyCLI struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
}

type verifyCheck struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type verifyFailedError struct{}

const verifyReportVersion = 2

func (verifyFailedError) Error() string {
	return "generated CLI verify failed"
}

func (verifyFailedError) SilentExitCode() int {
	return runtime.ExitGeneral
}

func verifyCmd(m *config.Manifest) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify generated CLI contract",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := verifyGenerated(cmd.Root(), m)
			var err error
			if jsonOut {
				err = writeJSON(cmd, report)
			} else {
				err = writeVerifyText(cmd, report)
			}
			if err != nil {
				return err
			}
			if !report.OK {
				return verifyFailedError{}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit verify JSON")
	return cmd
}

func verifyGenerated(root *cobra.Command, m *config.Manifest) verifyReport {
	version, commit, _ := VersionInfo()
	if commit == "none" {
		commit = ""
	}
	cliName := ""
	if m != nil {
		cliName = m.CLI.Name
	}
	if root != nil && root.Name() != "" {
		cliName = root.Name()
	}
	sources := runtime.SourceProvenanceOf(root)
	if sources == nil {
		sources = []runtime.SourceProvenance{}
	}
	report := verifyReport{
		Version: verifyReportVersion,
		OK:      true,
		Provenance: verifyProvenance{
			CLI: verifyCLI{
				Name:    cliName,
				Version: version,
				Commit:  commit,
			},
			SchemaVersion:        runtime.SchemaVersion,
			CatalogSchemaVersion: runtime.CatalogSchemaVersion,
			Sources:              sources,
		},
	}
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

func writeVerifyText(cmd *cobra.Command, report verifyReport) error {
	out := cmd.OutOrStdout()
	cli := report.Provenance.CLI
	if cli.Commit != "" {
		fmt.Fprintf(out, "CLI: %s %s (commit %s)\n", cli.Name, cli.Version, cli.Commit)
	} else {
		fmt.Fprintf(out, "CLI: %s %s\n", cli.Name, cli.Version)
	}
	fmt.Fprintf(out, "Schema: %d (catalog %d)\n", report.Provenance.SchemaVersion, report.Provenance.CatalogSchemaVersion)
	if len(report.Provenance.Sources) == 0 {
		fmt.Fprintln(out, "Sources: not recorded; regenerate with current lathe")
	} else {
		for _, src := range report.Provenance.Sources {
			fmt.Fprintln(out, formatVerifySource(src))
		}
	}
	passed, failed := 0, 0
	for _, check := range report.Checks {
		if check.OK {
			passed++
		} else {
			failed++
		}
	}
	fmt.Fprintf(out, "Checks: %d passed, %d failed\n", passed, failed)
	for _, check := range report.Checks {
		if !check.OK {
			fmt.Fprintf(out, "  FAIL %s: %s\n", check.Name, check.Error)
		}
	}
	return nil
}

func formatVerifySource(src runtime.SourceProvenance) string {
	if src.Kind == "local" {
		return fmt.Sprintf("Source %s: %s local, not reproducible (follows the local working tree)", src.ID, src.Backend)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Source %s: %s git", src.ID, src.Backend)
	if src.RepoURL != "" {
		fmt.Fprintf(&b, " %s", src.RepoURL)
	} else {
		b.WriteString(" (repository unknown)")
	}
	if src.PinnedTag != "" {
		fmt.Fprintf(&b, " %s", src.PinnedTag)
	}
	if src.ResolvedSHA != "" {
		fmt.Fprintf(&b, " @ %s", src.ResolvedSHA)
	}
	if src.Reproducible {
		b.WriteString(", reproducible")
	} else {
		b.WriteString(", not reproducible")
	}
	return b.String()
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
	schema := runtime.CatalogSchemaDocument()
	if catalog.CatalogSchemaVersion != schema.CatalogSchemaVersion {
		return fmt.Errorf("catalog schema = %d, want %d", catalog.CatalogSchemaVersion, schema.CatalogSchemaVersion)
	}
	if !reflect.DeepEqual(schema.Surfaces, []string{
		runtime.CatalogSurfaceCommands,
		runtime.CatalogSurfaceCommandsShow,
		runtime.CatalogSurfaceCommandsSchema,
		runtime.CatalogSurfaceSearch,
	}) {
		return fmt.Errorf("catalog schema surfaces = %#v", schema.Surfaces)
	}
	if schema.DryRun.Result != runtime.DryRunHTTPPreview {
		return fmt.Errorf("catalog schema dry_run.result = %q", schema.DryRun.Result)
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
	if err := verifyCatalogContract(entry); err != nil {
		return fmt.Errorf("%q %w", path, err)
	}
	if entry.Output.Binary != nil && cmd.Flags().Lookup(entry.Output.Binary.Flag) == nil {
		return fmt.Errorf("%q catalog binary requires missing --%s flag", path, entry.Output.Binary.Flag)
	}
	if entry.DryRun != nil && entry.DryRun.Mode == runtime.DryRunHTTPPreview {
		if runtime.WiredDryRunFlag(cmd) != entry.DryRun.Flag {
			return fmt.Errorf("%q catalog dry_run is not wired to the operation runner", path)
		}
		if cmd.Flags().Lookup(entry.DryRun.Flag) == nil {
			return fmt.Errorf("%q catalog dry_run requires missing --%s flag", path, entry.DryRun.Flag)
		}
	}
	return nil
}

func verifyCatalogContract(entry runtime.CatalogCommand) error {
	switch entry.Mutation {
	case runtime.MutationRead, runtime.MutationWrite, runtime.MutationUnknown:
	default:
		return fmt.Errorf("invalid mutation %q", entry.Mutation)
	}
	if entry.DryRun == nil {
		return fmt.Errorf("missing dry_run")
	}
	switch entry.DryRun.Mode {
	case runtime.DryRunUnsupported:
		if entry.DryRun.Flag != "" {
			return fmt.Errorf("unsupported dry_run must not declare a flag")
		}
	case runtime.DryRunHTTPPreview:
		if entry.Kind == "workflow" {
			return fmt.Errorf("workflow dry_run.mode = %q", entry.DryRun.Mode)
		}
		if entry.DryRun.Flag == "" {
			return fmt.Errorf("operation dry_run.flag is empty")
		}
	default:
		return fmt.Errorf("invalid dry_run.mode %q", entry.DryRun.Mode)
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
