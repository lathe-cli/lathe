package render

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

const (
	GeneratedRoot  = "internal/generated"
	ModulesGenFile = "internal/generated/modules_gen.go"
	SkillBundleDir = "internal/generated/skillbundle"
	WorkflowsDir   = "internal/generated/workflows"
)

type moduleCtx struct {
	Module        string
	CLIName       string
	RuntimePkg    string
	SchemaVersion int
	Ops           []runtime.CommandSpec
}

type ModuleMount struct {
	Name string
	Flat bool
}

type ModulesGenOptions struct {
	SkillBundle *SkillBundleMount
	Workflows   bool
}

type SkillBundleMount struct {
	Root string
}

type workflowCtx struct {
	RuntimePkg string
	Specs      []runtime.WorkflowSpec
}

// RuntimePkg is the import path downstream-generated modules use to reach
// lathe's runtime. Downstream forks import lathe as a library; they do not
// vendor or copy the runtime package into their own tree.
const RuntimePkg = "github.com/lathe-cli/lathe/pkg/runtime"

// RenderModule emits internal/generated/<name>/<name>_gen.go. overrides is
// keyed by command Use string; each override's non-empty fields are baked into
// the corresponding CommandSpec (Aliases are appended, not replaced). Passing
// a nil map means "no overlay", equivalent to the pre-overlay behavior.
// cliName is the name used in runtime.Build (the CLI-visible module path
// segment); if empty it falls back to name.
func RenderModule(name, cliName string, specs []runtime.CommandSpec, overrides map[string]overlay.Override) error {
	if cliName == "" {
		cliName = name
	}
	merged := MergeOverlayModule(specs, overlay.Module{Commands: overrides})
	return renderModuleSpecs(name, cliName, merged)
}

func ResolveFlatCommandPath(policy string, moduleCount int, specs []runtime.CommandSpec) (bool, error) {
	if err := validateCommandPaths(specs); err != nil {
		return false, err
	}
	if policy == "" {
		policy = config.CommandPathAuto
	}
	if moduleCount != 1 {
		if policy == config.CommandPathFlat {
			return false, fmt.Errorf("cli.command_path=flat requires exactly one source module")
		}
		return false, nil
	}
	switch policy {
	case config.CommandPathNamespaced:
		return false, nil
	case config.CommandPathFlat:
		if conflict, ok := flatPathConflict(specs); ok {
			return false, fmt.Errorf("cli.command_path=flat conflicts with command %q", conflict)
		}
		return true, nil
	case config.CommandPathAuto:
		_, conflict := flatPathConflict(specs)
		return !conflict, nil
	default:
		return false, fmt.Errorf("unknown cli.command_path %q", policy)
	}
}

func RewriteCommandExamples(cli, module string, specs []runtime.CommandSpec, flat bool) []runtime.CommandSpec {
	if cli == "" {
		return specs
	}
	rewritten := make([]runtime.CommandSpec, 0, len(specs))
	for _, spec := range specs {
		next := cloneCommandSpec(spec)
		if next.Example != "" {
			next.Example = commandExample(next.Example, cli, module, next, flat)
		}
		for i := range next.Examples {
			if next.Examples[i].Command != "" {
				next.Examples[i].Command = commandExample(next.Examples[i].Command, cli, module, next, flat)
			}
			for j := range next.Examples[i].FollowUpCommands {
				next.Examples[i].FollowUpCommands[j] = commandExample(next.Examples[i].FollowUpCommands[j], cli, module, next, flat)
			}
		}
		rewritten = append(rewritten, next)
	}
	return rewritten
}

func flatPathConflict(specs []runtime.CommandSpec) (string, bool) {
	seen := map[string]string{}
	for _, spec := range specs {
		name := rootCommandName(spec.Group)
		if name == "" {
			continue
		}
		if reservedRootCommands[name] {
			return name, true
		}
		if group, ok := seen[name]; ok && group != spec.Group {
			return name, true
		}
		seen[name] = spec.Group
	}
	return "", false
}

func validateCommandPaths(specs []runtime.CommandSpec) error {
	type commandName struct {
		spec  runtime.CommandSpec
		alias bool
		index int
	}
	seen := map[string]commandName{}
	for specIndex, spec := range specs {
		group := rootCommandName(spec.Group)
		use := commandUseName(spec.Use)
		if group == "" || use == "" {
			return fmt.Errorf("command %q has empty generated path", commandIdentity(spec))
		}
		names := append([]string{use}, spec.Aliases...)
		for i, raw := range names {
			name := commandUseName(raw)
			if name == "" {
				return fmt.Errorf("command %q has empty alias", commandIdentity(spec))
			}
			cmdPath := group + " " + name
			if prev, ok := seen[cmdPath]; ok {
				if prev.index == specIndex {
					continue
				}
				kind := "command path"
				if i > 0 || prev.alias {
					kind = "command alias path"
				}
				return fmt.Errorf("%s %q conflicts between %s and %s", kind, cmdPath, commandDebugIdentity(prev.spec), commandDebugIdentity(spec))
			}
			seen[cmdPath] = commandName{spec: spec, alias: i > 0, index: specIndex}
		}
	}
	return nil
}

func commandUseName(use string) string {
	fields := strings.Fields(use)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func commandIdentity(spec runtime.CommandSpec) string {
	if spec.OperationID != "" {
		return spec.OperationID
	}
	if spec.Group != "" || spec.Use != "" {
		return strings.TrimSpace(spec.Group + " " + spec.Use)
	}
	return spec.PathTpl
}

func commandDebugIdentity(spec runtime.CommandSpec) string {
	var parts []string
	if spec.OperationID != "" {
		parts = append(parts, fmt.Sprintf("operationId=%q", spec.OperationID))
	}
	if spec.Method != "" || spec.PathTpl != "" {
		parts = append(parts, fmt.Sprintf("http=%q", strings.TrimSpace(spec.Method+" "+spec.PathTpl)))
	}
	if spec.Group != "" || spec.Use != "" {
		parts = append(parts, fmt.Sprintf("command=%q", strings.TrimSpace(spec.Group+" "+spec.Use)))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%q", commandIdentity(spec))
	}
	return strings.Join(parts, ", ")
}

func rootCommandName(use string) string {
	fields := strings.Fields(strings.ToLower(use))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

var reservedRootCommands = map[string]bool{
	"__lathe":  true,
	"auth":     true,
	"commands": true,
	"help":     true,
	"login":    true,
	"search":   true,
	"skill":    true,
	"update":   true,
}

// ValidateModuleNames rejects namespaced module mount names that would shadow
// a reserved root command or another module on the generated root command.
func ValidateModuleNames(names []string) error {
	seen := map[string]bool{}
	for _, raw := range names {
		name := rootCommandName(raw)
		if name == "" {
			return fmt.Errorf("module name %q has empty generated root command", raw)
		}
		if reservedRootCommands[name] {
			return fmt.Errorf("module name %q conflicts with a reserved root command", raw)
		}
		if seen[name] {
			return fmt.Errorf("module name %q is mounted more than once", raw)
		}
		seen[name] = true
	}
	return nil
}

func MergeOverlay(specs []runtime.CommandSpec, overrides map[string]overlay.Override) []runtime.CommandSpec {
	return MergeOverlayModule(specs, overlay.Module{Commands: overrides})
}

func MergeOverlayModule(specs []runtime.CommandSpec, mod overlay.Module) []runtime.CommandSpec {
	var merged []runtime.CommandSpec
	for _, s := range specs {
		o, ok := mod.Commands[s.Use]
		if ok && !overrideMatches(s, o) {
			ok = false
		}
		if ok && o.Ignore {
			continue
		}
		cs := cloneCommandSpec(s)
		applyBulkDefaults(&cs, mod.Defaults)
		if !ok {
			merged = append(merged, cs)
			continue
		}
		applyCommandOverride(&cs, o)
		merged = append(merged, cs)
	}
	return merged
}

func overrideMatches(spec runtime.CommandSpec, override overlay.Override) bool {
	if override.Match.Method != "" && !strings.EqualFold(override.Match.Method, spec.Method) {
		return false
	}
	if override.Match.Path != "" && override.Match.Path != spec.PathTpl {
		return false
	}
	return true
}

func cloneCommandSpec(spec runtime.CommandSpec) runtime.CommandSpec {
	cloned := spec
	cloned.Aliases = append([]string(nil), spec.Aliases...)
	cloned.Examples = append([]runtime.CommandExample(nil), spec.Examples...)
	for i := range cloned.Examples {
		cloned.Examples[i].FollowUpCommands = append([]string(nil), spec.Examples[i].FollowUpCommands...)
	}
	cloned.Shortcuts = append([]runtime.CommandShortcut(nil), spec.Shortcuts...)
	for i := range cloned.Shortcuts {
		cloned.Shortcuts[i].Params = copyStringMap(spec.Shortcuts[i].Params)
	}
	cloned.Notes = append([]string(nil), spec.Notes...)
	cloned.Prerequisites = append([]string(nil), spec.Prerequisites...)
	cloned.KnownErrors = append([]runtime.KnownError(nil), spec.KnownErrors...)
	cloned.Params = append([]runtime.ParamSpec(nil), spec.Params...)
	for i := range cloned.Params {
		cloned.Params[i].Enum = append([]string(nil), spec.Params[i].Enum...)
	}
	return cloned
}

func applyBulkDefaults(spec *runtime.CommandSpec, defaults overlay.Defaults) {
	if defaults.Pagination == nil || !matchesAny(defaults.Pagination.MatchCommands, spec.Use) {
		return
	}
	for i := range spec.Params {
		if spec.Params[i].Default != "" {
			continue
		}
		if value, ok := defaults.Pagination.Params[spec.Params[i].Name]; ok {
			spec.Params[i].Default = value
		}
	}
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		matched, err := path.Match(pattern, value)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func applyCommandOverride(spec *runtime.CommandSpec, override overlay.Override) {
	if override.Use != "" {
		spec.Use = override.Use
	}
	if override.Short != "" {
		spec.Short = override.Short
	}
	if override.Long != "" {
		spec.Long = override.Long
	}
	if override.Example != "" {
		spec.Example = override.Example
	}
	if len(override.Examples) > 0 {
		spec.Examples = make([]runtime.CommandExample, 0, len(override.Examples))
		for _, example := range override.Examples {
			spec.Examples = append(spec.Examples, runtimeCommandExample(example))
		}
	}
	if len(override.Notes) > 0 {
		spec.Notes = append([]string(nil), override.Notes...)
	}
	if len(override.Prerequisites) > 0 {
		spec.Prerequisites = append([]string(nil), override.Prerequisites...)
	}
	if len(override.KnownErrors) > 0 {
		spec.KnownErrors = make([]runtime.KnownError, 0, len(override.KnownErrors))
		for _, ke := range override.KnownErrors {
			spec.KnownErrors = append(spec.KnownErrors, runtime.KnownError{Status: ke.Status, Cause: ke.Cause})
		}
	}
	if len(override.Aliases) > 0 {
		spec.Aliases = append(spec.Aliases, override.Aliases...)
	}
	for _, shortcut := range override.Shortcuts {
		spec.Shortcuts = append(spec.Shortcuts, runtime.CommandShortcut{
			Use:    shortcut.Use,
			Params: copyStringMap(shortcut.Params),
		})
	}
	if override.Group != "" {
		spec.Group = override.Group
	}
	if override.Hidden != nil {
		spec.Hidden = *override.Hidden
	}
	if len(override.Params) > 0 {
		for j := range spec.Params {
			po, pok := override.Params[spec.Params[j].Name]
			if !pok {
				continue
			}
			if po.Flag != "" {
				spec.Params[j].Flag = po.Flag
			}
			if po.Help != "" {
				spec.Params[j].Help = po.Help
			}
			if po.Required {
				spec.Params[j].Required = true
			}
			if po.Default != "" {
				spec.Params[j].Default = po.Default
			}
			if po.Deprecated || po.DeprecatedAlias {
				spec.Params[j].Deprecated = true
			}
		}
	}
}

func runtimeCommandExample(example overlay.Example) runtime.CommandExample {
	var bodyShape json.RawMessage
	if len(example.BodyShape) > 0 {
		bodyShape, _ = json.Marshal(example.BodyShape)
	}
	out := runtime.CommandExample{
		Summary:          example.Summary,
		Command:          example.Command,
		BodyShape:        bodyShape,
		FollowUpCommands: append([]string(nil), example.FollowUpCommands...),
	}
	if example.OutputHints.IDPath != "" || example.OutputHints.ListPath != "" {
		out.OutputHints = &runtime.ExampleOutputHints{
			IDPath:   example.OutputHints.IDPath,
			ListPath: example.OutputHints.ListPath,
		}
	}
	return out
}

func ValidateShortcuts(moduleNames []string, specs []runtime.CommandSpec, flat bool) error {
	rootNames := make([]string, 0, len(reservedRootCommands)+len(moduleNames)+len(specs))
	for name := range reservedRootCommands {
		rootNames = append(rootNames, name)
	}
	if flat {
		seen := map[string]bool{}
		for _, spec := range specs {
			name := rootCommandName(spec.Group)
			if !seen[name] {
				rootNames = append(rootNames, name)
				seen[name] = true
			}
		}
		return runtime.ValidateShortcuts(specs, rootNames)
	}
	rootNames = append(rootNames, moduleNames...)
	return runtime.ValidateShortcuts(specs, rootNames)
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func renderModuleSpecs(name, cliName string, specs []runtime.CommandSpec) error {
	var buf strings.Builder
	ctx := moduleCtx{
		Module:        name,
		CLIName:       cliName,
		RuntimePkg:    RuntimePkg,
		SchemaVersion: runtime.SchemaVersion,
		Ops:           specs,
	}
	if err := moduleTmpl.Execute(&buf, ctx); err != nil {
		return err
	}
	outPath := filepath.Join(GeneratedRoot, name, name+"_gen.go")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(outPath+".unformatted", []byte(buf.String()), 0o644)
		return err
	}
	if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d commands\n", outPath, len(specs))
	return nil
}

func RenderModulesGen(modules []ModuleMount) error {
	return RenderModulesGenWithOptions(modules, ModulesGenOptions{})
}

func RenderModulesGenWithOptions(modules []ModuleMount, opts ModulesGenOptions) error {
	mp, err := modulePath()
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := modulesTmpl.Execute(&buf, struct {
		Prefix      string
		Modules     []ModuleMount
		SkillBundle *SkillBundleMount
		Workflows   bool
	}{Prefix: mp + "/internal/generated/", Modules: modules, SkillBundle: opts.SkillBundle, Workflows: opts.Workflows}); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(ModulesGenFile+".unformatted", []byte(buf.String()), 0o644)
		return err
	}
	if err := os.WriteFile(ModulesGenFile, formatted, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d modules\n", ModulesGenFile, len(modules))
	return nil
}

func RenderWorkflows(specs []runtime.WorkflowSpec) error {
	var buf strings.Builder
	if err := workflowsTmpl.Execute(&buf, workflowCtx{RuntimePkg: RuntimePkg, Specs: specs}); err != nil {
		return err
	}
	outPath := filepath.Join(WorkflowsDir, "workflows_gen.go")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(outPath+".unformatted", []byte(buf.String()), 0o644)
		return err
	}
	if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d workflows\n", outPath, len(specs))
	return nil
}

func RemoveWorkflowsPackage() error {
	return os.RemoveAll(WorkflowsDir)
}

func RenderSkillBundlePackage(skillDir string, cliName string) error {
	root := SkillDirName(cliName)
	dst := filepath.Join(SkillBundleDir, root)
	if err := os.MkdirAll(SkillBundleDir, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := copySkillBundleFiles(skillDir, dst); err != nil {
		return err
	}
	var buf strings.Builder
	if err := skillBundleTmpl.Execute(&buf, struct{ Root string }{Root: root}); err != nil {
		return err
	}
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		_ = os.WriteFile(filepath.Join(SkillBundleDir, "skillbundle_gen.go.unformatted"), []byte(buf.String()), 0o644)
		return err
	}
	if err := os.WriteFile(filepath.Join(SkillBundleDir, "skillbundle_gen.go"), formatted, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", SkillBundleDir)
	return nil
}

func RemoveSkillBundlePackage() error {
	return os.RemoveAll(SkillBundleDir)
}

func copySkillBundleFiles(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func schemaLiteral(s *runtime.SchemaSpec) string {
	if s == nil {
		return "nil"
	}
	var b strings.Builder
	writeSchemaLiteral(&b, s)
	return b.String()
}

func stringMapLiteral(values map[string]string) string {
	if len(values) == 0 {
		return "nil"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("map[string]string{")
	for _, key := range keys {
		fmt.Fprintf(&b, "%q: %q,", key, values[key])
	}
	b.WriteByte('}')
	return b.String()
}

func workflowSpecLiteral(spec runtime.WorkflowSpec) string {
	var b strings.Builder
	b.WriteString("runtime.WorkflowSpec{")
	writeStringField(&b, "Use", spec.Use)
	writeStringSliceField(&b, "Aliases", spec.Aliases)
	writeStringField(&b, "Short", spec.Short)
	writeStringField(&b, "Long", spec.Long)
	writeStringField(&b, "Example", spec.Example)
	writeBoolField(&b, "Hidden", spec.Hidden)
	writeBoolField(&b, "Deprecated", spec.Deprecated)
	if len(spec.Params) > 0 {
		fmt.Fprintf(&b, "Params: %s,", paramSpecsLiteral(spec.Params))
	}
	if len(spec.Steps) > 0 {
		fmt.Fprintf(&b, "Steps: %s,", workflowStepSpecsLiteral(spec.Steps))
	}
	writeStringField(&b, "OutputFrom", spec.OutputFrom)
	if outputHintsSet(spec.Output) {
		fmt.Fprintf(&b, "Output: %s,", outputHintsLiteral(spec.Output))
	}
	b.WriteByte('}')
	return b.String()
}

func commandSpecLiteral(spec runtime.CommandSpec) string {
	var b strings.Builder
	b.WriteString("runtime.CommandSpec{")
	writeStringField(&b, "Group", spec.Group)
	writeStringField(&b, "Use", spec.Use)
	writeStringSliceField(&b, "Aliases", spec.Aliases)
	if len(spec.Shortcuts) > 0 {
		fmt.Fprintf(&b, "Shortcuts: %s,", commandShortcutsLiteral(spec.Shortcuts))
	}
	writeStringField(&b, "Short", spec.Short)
	writeStringField(&b, "Long", spec.Long)
	writeStringField(&b, "Example", spec.Example)
	if len(spec.Examples) > 0 {
		fmt.Fprintf(&b, "Examples: %s,", commandExamplesLiteral(spec.Examples))
	}
	writeStringField(&b, "OperationID", spec.OperationID)
	writeBoolField(&b, "Hidden", spec.Hidden)
	writeBoolField(&b, "Deprecated", spec.Deprecated)
	writeStringField(&b, "Method", spec.Method)
	writeStringField(&b, "PathTpl", spec.PathTpl)
	writeStringField(&b, "DefaultHostname", spec.DefaultHostname)
	if len(spec.Params) > 0 {
		fmt.Fprintf(&b, "Params: %s,", paramSpecsLiteral(spec.Params))
	}
	if spec.RequestBody != nil {
		fmt.Fprintf(&b, "RequestBody: %s,", requestBodyLiteral(spec.RequestBody))
	}
	if outputHintsSet(spec.Output) {
		fmt.Fprintf(&b, "Output: %s,", outputHintsLiteral(spec.Output))
	}
	if spec.Security != nil {
		fmt.Fprintf(&b, "Security: %s,", securityHintLiteral(spec.Security))
	}
	writeStringSliceField(&b, "Notes", spec.Notes)
	writeStringSliceField(&b, "Prerequisites", spec.Prerequisites)
	if len(spec.KnownErrors) > 0 {
		fmt.Fprintf(&b, "KnownErrors: %s,", knownErrorsLiteral(spec.KnownErrors))
	}
	b.WriteByte('}')
	return b.String()
}

func workflowStepSpecsLiteral(steps []runtime.WorkflowStepSpec) string {
	var b strings.Builder
	b.WriteString("[]runtime.WorkflowStepSpec{")
	for _, step := range steps {
		b.WriteString("runtime.WorkflowStepSpec{")
		writeStringField(&b, "ID", step.ID)
		fmt.Fprintf(&b, "Operation: %s,", commandSpecLiteral(step.Operation))
		if len(step.When) > 0 {
			fmt.Fprintf(&b, "When: %s,", workflowConditionsLiteral(step.When))
		}
		if len(step.Params) > 0 {
			fmt.Fprintf(&b, "Params: %s,", stringMapLiteral(step.Params))
		}
		if len(step.BodySets) > 0 {
			fmt.Fprintf(&b, "BodySets: %s,", workflowValuesLiteral(step.BodySets))
		}
		if len(step.BodyStringSets) > 0 {
			fmt.Fprintf(&b, "BodyStringSets: %s,", workflowValuesLiteral(step.BodyStringSets))
		}
		b.WriteString("},")
	}
	b.WriteByte('}')
	return b.String()
}

func workflowConditionsLiteral(conditions []runtime.WorkflowCondition) string {
	var b strings.Builder
	b.WriteString("[]runtime.WorkflowCondition{")
	for _, cond := range conditions {
		b.WriteString("runtime.WorkflowCondition{")
		writeStringField(&b, "Value", cond.Value)
		writeStringField(&b, "Operator", cond.Operator)
		b.WriteString("Values: []string{")
		for _, value := range cond.Values {
			fmt.Fprintf(&b, "%q,", value)
		}
		b.WriteString("},},")
	}
	b.WriteByte('}')
	return b.String()
}

func workflowValuesLiteral(values []runtime.WorkflowValue) string {
	var b strings.Builder
	b.WriteString("[]runtime.WorkflowValue{")
	for _, value := range values {
		b.WriteString("runtime.WorkflowValue{")
		writeStringField(&b, "Name", value.Name)
		writeStringField(&b, "Value", value.Value)
		b.WriteString("},")
	}
	b.WriteByte('}')
	return b.String()
}

func paramSpecsLiteral(params []runtime.ParamSpec) string {
	var b strings.Builder
	b.WriteString("[]runtime.ParamSpec{")
	for _, param := range params {
		b.WriteString("runtime.ParamSpec{")
		writeStringField(&b, "Name", param.Name)
		writeStringField(&b, "Flag", param.Flag)
		writeStringField(&b, "In", param.In)
		writeStringField(&b, "GoType", param.GoType)
		writeStringField(&b, "Help", param.Help)
		writeBoolField(&b, "Required", param.Required)
		writeStringField(&b, "Default", param.Default)
		writeStringSliceField(&b, "Enum", param.Enum)
		writeStringField(&b, "Format", param.Format)
		writeBoolField(&b, "Deprecated", param.Deprecated)
		b.WriteString("},")
	}
	b.WriteByte('}')
	return b.String()
}

func commandShortcutsLiteral(shortcuts []runtime.CommandShortcut) string {
	var b strings.Builder
	b.WriteString("[]runtime.CommandShortcut{")
	for _, shortcut := range shortcuts {
		b.WriteString("runtime.CommandShortcut{")
		writeStringField(&b, "Use", shortcut.Use)
		if len(shortcut.Params) > 0 {
			fmt.Fprintf(&b, "Params: %s,", stringMapLiteral(shortcut.Params))
		}
		b.WriteString("},")
	}
	b.WriteByte('}')
	return b.String()
}

func commandExamplesLiteral(examples []runtime.CommandExample) string {
	var b strings.Builder
	b.WriteString("[]runtime.CommandExample{")
	for _, example := range examples {
		b.WriteString("runtime.CommandExample{")
		writeStringField(&b, "Summary", example.Summary)
		writeStringField(&b, "Command", example.Command)
		if len(example.BodyShape) > 0 {
			fmt.Fprintf(&b, "BodyShape: []byte(%q),", string(example.BodyShape))
		}
		if example.OutputHints != nil {
			fmt.Fprintf(&b, "OutputHints: %s,", exampleOutputHintsLiteral(example.OutputHints))
		}
		writeStringSliceField(&b, "FollowUpCommands", example.FollowUpCommands)
		b.WriteString("},")
	}
	b.WriteByte('}')
	return b.String()
}

func exampleOutputHintsLiteral(hints *runtime.ExampleOutputHints) string {
	var b strings.Builder
	b.WriteString("&runtime.ExampleOutputHints{")
	writeStringField(&b, "IDPath", hints.IDPath)
	writeStringField(&b, "ListPath", hints.ListPath)
	b.WriteByte('}')
	return b.String()
}

func requestBodyLiteral(body *runtime.RequestBody) string {
	var b strings.Builder
	b.WriteString("&runtime.RequestBody{")
	writeBoolField(&b, "Required", body.Required)
	writeStringField(&b, "MediaType", body.MediaType)
	if body.Schema != nil {
		fmt.Fprintf(&b, "Schema: %s,", schemaLiteral(body.Schema))
	}
	writeStringField(&b, "Template", body.Template)
	writeStringField(&b, "MergePath", body.MergePath)
	b.WriteByte('}')
	return b.String()
}

func outputHintsLiteral(hints runtime.OutputHints) string {
	var b strings.Builder
	b.WriteString("runtime.OutputHints{")
	writeStringField(&b, "ListPath", hints.ListPath)
	writeStringSliceField(&b, "DefaultColumns", hints.DefaultColumns)
	writeStringField(&b, "ResponseMediaType", hints.ResponseMediaType)
	if hints.Pagination != nil {
		fmt.Fprintf(&b, "Pagination: %s,", paginationHintLiteral(hints.Pagination))
	}
	if hints.Streaming != nil {
		fmt.Fprintf(&b, "Streaming: %s,", streamingHintLiteral(hints.Streaming))
	}
	b.WriteByte('}')
	return b.String()
}

func paginationHintLiteral(hint *runtime.PaginationHint) string {
	var b strings.Builder
	b.WriteString("&runtime.PaginationHint{")
	writeStringField(&b, "Strategy", hint.Strategy)
	writeStringField(&b, "TokenParam", hint.TokenParam)
	writeStringField(&b, "TokenField", hint.TokenField)
	writeStringField(&b, "LimitParam", hint.LimitParam)
	b.WriteByte('}')
	return b.String()
}

func streamingHintLiteral(hint *runtime.StreamingHint) string {
	var b strings.Builder
	b.WriteString("&runtime.StreamingHint{")
	writeStringField(&b, "Strategy", hint.Strategy)
	b.WriteByte('}')
	return b.String()
}

func securityHintLiteral(hint *runtime.SecurityHint) string {
	var b strings.Builder
	b.WriteString("&runtime.SecurityHint{")
	writeBoolField(&b, "Public", hint.Public)
	writeStringSliceField(&b, "Scopes", hint.Scopes)
	b.WriteByte('}')
	return b.String()
}

func knownErrorsLiteral(errors []runtime.KnownError) string {
	var b strings.Builder
	b.WriteString("[]runtime.KnownError{")
	for _, known := range errors {
		b.WriteString("runtime.KnownError{")
		if known.Status != 0 {
			fmt.Fprintf(&b, "Status: %d,", known.Status)
		}
		writeStringField(&b, "Cause", known.Cause)
		b.WriteString("},")
	}
	b.WriteByte('}')
	return b.String()
}

func outputHintsSet(hints runtime.OutputHints) bool {
	return hints.ListPath != "" || len(hints.DefaultColumns) > 0 || hints.ResponseMediaType != "" || hints.Pagination != nil || hints.Streaming != nil
}

func writeStringField(b *strings.Builder, name, value string) {
	if value != "" {
		fmt.Fprintf(b, "%s: %q,", name, value)
	}
}

func writeBoolField(b *strings.Builder, name string, value bool) {
	if value {
		fmt.Fprintf(b, "%s: true,", name)
	}
}

func writeStringSliceField(b *strings.Builder, name string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s: ", name)
	b.WriteString("[]string{")
	for _, value := range values {
		fmt.Fprintf(b, "%q,", value)
	}
	b.WriteString("},")
}

func writeSchemaLiteral(b *strings.Builder, s *runtime.SchemaSpec) {
	if s == nil {
		b.WriteString("nil")
		return
	}
	b.WriteString("&runtime.SchemaSpec{")
	if s.Ref != "" {
		fmt.Fprintf(b, "Ref: %q,", s.Ref)
	}
	if s.Type != "" {
		fmt.Fprintf(b, "Type: %q,", s.Type)
	}
	if len(s.Properties) > 0 {
		b.WriteString("Properties: map[string]*runtime.SchemaSpec{")
		keys := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "%q: ", k)
			writeSchemaLiteral(b, s.Properties[k])
			b.WriteByte(',')
		}
		b.WriteString("},")
	}
	if len(s.Required) > 0 {
		b.WriteString("Required: []string{")
		for _, name := range s.Required {
			fmt.Fprintf(b, "%q,", name)
		}
		b.WriteString("},")
	}
	if s.Items != nil {
		b.WriteString("Items: ")
		writeSchemaLiteral(b, s.Items)
		b.WriteByte(',')
	}
	b.WriteByte('}')
}

var moduleTmpl = template.Must(template.New("gen").Funcs(template.FuncMap{
	"schemaLiteral":    schemaLiteral,
	"stringMapLiteral": stringMapLiteral,
}).Parse(`// Code generated by lathe codegen. DO NOT EDIT.

package {{.Module}}

import (
	"github.com/spf13/cobra"

	"{{.RuntimePkg}}"
)

const generatedSchemaVersion = {{.SchemaVersion}}

func Mount(root *cobra.Command) error {
	if err := runtime.AssertSchema(generatedSchemaVersion); err != nil {
		return err
	}
	return runtime.Build(root, {{printf "%q" .CLIName}}, Specs)
}

func MountFlat(root *cobra.Command) error {
	if err := runtime.AssertSchema(generatedSchemaVersion); err != nil {
		return err
	}
	return runtime.BuildFlat(root, {{printf "%q" .CLIName}}, Specs)
}

var Specs = []runtime.CommandSpec{
{{- range $op := .Ops}}
	{
		Group:       {{printf "%q" $op.Group}},
		Use:         {{printf "%q" $op.Use}},
		{{- if $op.Aliases}}
		Aliases:     []string{ {{- range $op.Aliases}}{{printf "%q" .}}, {{end -}} },
		{{- end}}
		{{- if $op.Shortcuts}}
		Shortcuts: []runtime.CommandShortcut{
			{{- range $shortcut := $op.Shortcuts}}
			{Use: {{printf "%q" $shortcut.Use}}{{if $shortcut.Params}}, Params: {{stringMapLiteral $shortcut.Params}}{{end}}},
			{{- end}}
		},
		{{- end}}
		Short:       {{printf "%q" $op.Short}},
		{{- if $op.Long}}
		Long:        {{printf "%q" $op.Long}},
		{{- end}}
			{{- if $op.Example}}
			Example:     {{printf "%q" $op.Example}},
			{{- end}}
			{{- if $op.Examples}}
			Examples: []runtime.CommandExample{
				{{- range $example := $op.Examples}}
				{
					{{- if $example.Summary}}Summary: {{printf "%q" $example.Summary}},{{end}}
					{{- if $example.Command}}Command: {{printf "%q" $example.Command}},{{end}}
					{{- if $example.BodyShape}}BodyShape: []byte({{printf "%q" $example.BodyShape}}),{{end}}
					{{- if $example.OutputHints}}OutputHints: &runtime.ExampleOutputHints{
						{{- if $example.OutputHints.IDPath}}IDPath: {{printf "%q" $example.OutputHints.IDPath}},{{end}}
						{{- if $example.OutputHints.ListPath}}ListPath: {{printf "%q" $example.OutputHints.ListPath}},{{end}}
					},{{end}}
					{{- if $example.FollowUpCommands}}FollowUpCommands: []string{
						{{- range $example.FollowUpCommands}}{{printf "%q" .}},{{end}}
					},{{end}}
				},
				{{- end}}
			},
			{{- end}}
			{{- if $op.Notes}}
		Notes: []string{
			{{- range $op.Notes}}{{printf "%q" .}},{{end}}
		},
		{{- end}}
		{{- if $op.Prerequisites}}
		Prerequisites: []string{
			{{- range $op.Prerequisites}}{{printf "%q" .}},{{end}}
		},
		{{- end}}
		{{- if $op.KnownErrors}}
		KnownErrors: []runtime.KnownError{
			{{- range $op.KnownErrors}}
			{Status: {{.Status}}, Cause: {{printf "%q" .Cause}}},
			{{- end}}
		},
		{{- end}}
		{{- if $op.OperationID}}
		OperationID: {{printf "%q" $op.OperationID}},
		{{- end}}
		{{- if $op.Hidden}}
		Hidden:      true,
		{{- end}}
		{{- if $op.Deprecated}}
		Deprecated:  true,
		{{- end}}
		Method:      {{printf "%q" $op.Method}},
		PathTpl:     {{printf "%q" $op.PathTpl}},
		{{- if $op.DefaultHostname}}
		DefaultHostname: {{printf "%q" $op.DefaultHostname}},
		{{- end}}
		{{- if $op.Params}}
		Params: []runtime.ParamSpec{
			{{- range $op.Params}}
			{Name: {{printf "%q" .Name}}, Flag: {{printf "%q" .Flag}}, In: {{printf "%q" .In}}, GoType: {{printf "%q" .GoType}}, Help: {{printf "%q" .Help}}, Required: {{.Required}}{{- if .Default}}, Default: {{printf "%q" .Default}}{{end}}{{- if .Enum}}, Enum: []string{ {{- range .Enum}}{{printf "%q" .}}, {{end -}} }{{end}}{{- if .Format}}, Format: {{printf "%q" .Format}}{{end}}{{- if .Deprecated}}, Deprecated: true{{end}}},
			{{- end}}
		},
		{{- end}}
		{{- if $op.RequestBody}}
			RequestBody: &runtime.RequestBody{
				Required: {{$op.RequestBody.Required}},
				{{- if $op.RequestBody.MediaType}}
				MediaType: {{printf "%q" $op.RequestBody.MediaType}},
				{{- end}}
				{{- if $op.RequestBody.Schema}}
				Schema: {{schemaLiteral $op.RequestBody.Schema}},
				{{- end}}
				{{- if $op.RequestBody.Template}}
				Template: {{printf "%q" $op.RequestBody.Template}},
				{{- end}}
				{{- if $op.RequestBody.MergePath}}
				MergePath: {{printf "%q" $op.RequestBody.MergePath}},
				{{- end}}
			},
			{{- end}}
		{{- if or $op.Output.ListPath $op.Output.DefaultColumns $op.Output.ResponseMediaType $op.Output.Pagination $op.Output.Streaming}}
		Output: runtime.OutputHints{
			{{- if $op.Output.ListPath}}ListPath: {{printf "%q" $op.Output.ListPath}},{{end}}
			{{- if $op.Output.DefaultColumns}}DefaultColumns: []string{
				{{- range $op.Output.DefaultColumns}}{{printf "%q" .}},{{end}}
			},{{end}}
			{{- if $op.Output.ResponseMediaType}}ResponseMediaType: {{printf "%q" $op.Output.ResponseMediaType}},{{end}}
			{{- if $op.Output.Pagination}}Pagination: &runtime.PaginationHint{
				Strategy: {{printf "%q" $op.Output.Pagination.Strategy}},
				{{- if $op.Output.Pagination.TokenParam}}TokenParam: {{printf "%q" $op.Output.Pagination.TokenParam}},{{end}}
				{{- if $op.Output.Pagination.TokenField}}TokenField: {{printf "%q" $op.Output.Pagination.TokenField}},{{end}}
				{{- if $op.Output.Pagination.LimitParam}}LimitParam: {{printf "%q" $op.Output.Pagination.LimitParam}},{{end}}
			},{{end}}
			{{- if $op.Output.Streaming}}Streaming: &runtime.StreamingHint{
				Strategy: {{printf "%q" $op.Output.Streaming.Strategy}},
			},{{end}}
		},
		{{- end}}
		{{- if $op.Security}}
		Security: &runtime.SecurityHint{
			{{- if $op.Security.Public}}Public: true,{{end}}
			{{- if $op.Security.Scopes}}Scopes: []string{
				{{- range $op.Security.Scopes}}{{printf "%q" .}},{{end}}
			},{{end}}
		},
		{{- end}}
	},
{{- end}}
}
`))

var modulesTmpl = template.Must(template.New("modules").Parse(`// Code generated by lathe codegen. DO NOT EDIT.

package generated

import (
{{- if .SkillBundle}}
	lathekitup "github.com/lathe-cli/kitup/go"
	lathekitupcobra "github.com/lathe-cli/kitup/go-cobra"
	latheruntime "github.com/lathe-cli/lathe/pkg/runtime"
{{- end}}
	"github.com/spf13/cobra"

{{- range .Modules}}
	{{.Name}} "{{$.Prefix}}{{.Name}}"
{{- end}}
{{- if .Workflows}}
	lathegeneratedworkflows "{{$.Prefix}}workflows"
{{- end}}
{{- if .SkillBundle}}
	lathegeneratedskillbundle "{{$.Prefix}}skillbundle"
{{- end}}
)

// Mount mounts every generated command and capability declared by codegen.
func Mount(root *cobra.Command) error {
	return MountModules(root)
}

// MountModules mounts every module and generated capability under root.
// The import list above is the single source of truth for which modules
// are compiled into this binary. main.go wires this call after
// app.NewApp() so the framework package never imports downstream code.
func MountModules(root *cobra.Command) error {
{{- range .Modules}}
	if err := {{.Name}}.{{if .Flat}}MountFlat{{else}}Mount{{end}}(root); err != nil {
		return err
	}
{{- end}}
{{- if .Workflows}}
	if err := lathegeneratedworkflows.Mount(root); err != nil {
		return err
	}
{{- end}}
{{- if .SkillBundle}}
	latheruntime.AttachCapability(root, latheruntime.CapabilitySkillBundle)
	root.AddCommand(lathekitupcobra.NewSkillCommand(lathekitupcobra.Options{
		AppID:  root.Name(),
		Bundle: lathekitup.FSBundle(lathegeneratedskillbundle.FS, lathegeneratedskillbundle.Root),
	}))
{{- end}}
	return nil
}
`))

var workflowsTmpl = template.Must(template.New("workflows").Funcs(template.FuncMap{
	"workflowSpecLiteral": workflowSpecLiteral,
}).Parse(`// Code generated by lathe codegen. DO NOT EDIT.

package workflows

import (
	"github.com/spf13/cobra"

	"{{.RuntimePkg}}"
)

func Mount(root *cobra.Command) error {
	return runtime.BuildWorkflows(root, Specs)
}

var Specs = []runtime.WorkflowSpec{
{{- range .Specs}}
	{{workflowSpecLiteral .}},
{{- end}}
}
`))

var skillBundleTmpl = template.Must(template.New("skillbundle").Parse(`// Code generated by lathe codegen. DO NOT EDIT.

package skillbundle

import "embed"

const Root = {{printf "%q" .Root}}

//go:embed {{.Root}}/**
var FS embed.FS
`))

// modulePath reads the `module` directive from go.mod in the current working
// directory. Codegen uses this to compute the downstream's own generated/
// package import prefix so a downstream fork can rename its Go module without
// touching codegen source. The runtime package is always imported from lathe
// itself (see RuntimePkg).
func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module directive in go.mod")
}
