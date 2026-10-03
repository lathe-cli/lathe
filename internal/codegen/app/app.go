// Package app models the generated CLI application that one codegen run is
// about to emit. Outputs are collected into an App first and written together,
// so a failing input never leaves partially generated output behind. The model
// is internal to codegen; it is not a stable extension API.
package app

import (
	"fmt"
	"maps"
	"mime"
	"slices"

	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

// App is the complete set of generated outputs for one codegen run.
type App struct {
	Manifest  *config.Manifest
	Modules   []Module
	Workflows []runtime.WorkflowSpec
	Skill     *Skill
}

// Module is one generated command module and how it mounts on the root command.
type Module struct {
	Source     string
	CLIName    string
	Flat       bool
	Specs      []runtime.CommandSpec
	Provenance runtime.SourceProvenance
}

// Skill is the optional generated Skill directory output.
type Skill struct {
	Dir     string
	Include render.SkillInclude
	Modules []render.SkillModule
	Bundle  bool
}

// Validate rejects app compositions that would produce a conflicting root
// command tree. Flat modules are skipped: their module name is never mounted,
// and flat root conflicts are rejected by ResolveFlatCommandPath.
func (a *App) Validate() error {
	names := make([]string, 0, len(a.Modules))
	for _, m := range a.Modules {
		if m.Flat {
			continue
		}
		names = append(names, m.CLIName)
	}
	for _, workflow := range a.Workflows {
		names = append(names, workflow.Use)
		names = append(names, workflow.Aliases...)
	}
	if err := render.ValidateModuleNames(names); err != nil {
		return err
	}
	for _, module := range a.Modules {
		for _, spec := range module.Specs {
			if err := validateCommandContexts(a.Manifest, spec); err != nil {
				return fmt.Errorf("command %q: %w", spec.Use, err)
			}
			if err := validateMultipartBody(spec); err != nil {
				return fmt.Errorf("command %q: %w", spec.Use, err)
			}
		}
	}
	for _, workflow := range a.Workflows {
		for _, step := range workflow.Steps {
			if err := validateCommandContexts(a.Manifest, step.Operation); err != nil {
				return fmt.Errorf("workflow %q step %q: %w", workflow.Use, step.ID, err)
			}
		}
	}
	return nil
}

func validateCommandContexts(manifest *config.Manifest, spec runtime.CommandSpec) error {
	contexts := map[string]config.ContextInfo(nil)
	if manifest != nil {
		contexts = manifest.Contexts
	}
	for _, param := range spec.Params {
		if param.Context == "" {
			continue
		}
		if _, ok := contexts[param.Context]; !ok {
			return fmt.Errorf("parameter %q references unknown context %q", param.Name, param.Context)
		}
		if param.GoType != "string" {
			return fmt.Errorf("context parameter %q must be a string", param.Name)
		}
	}
	if spec.SetContext == nil {
		return nil
	}
	if _, ok := contexts[spec.SetContext.Name]; !ok {
		return fmt.Errorf("sets unknown context %q", spec.SetContext.Name)
	}
	matches := 0
	for _, param := range spec.Params {
		if param.Name == spec.SetContext.Param || param.Flag == spec.SetContext.Param {
			matches++
			if param.GoType != "string" {
				return fmt.Errorf("context source parameter %q must be a string", spec.SetContext.Param)
			}
		}
	}
	if matches != 1 {
		return fmt.Errorf("context source parameter %q must match exactly one operation parameter", spec.SetContext.Param)
	}
	return nil
}

func validateMultipartBody(spec runtime.CommandSpec) error {
	if spec.RequestBody == nil || !multipartMediaType(spec.RequestBody.MediaType) {
		return nil
	}
	unsupported := make(map[string]bool, len(spec.RequestBody.UnsupportedFields))
	for _, name := range spec.RequestBody.UnsupportedFields {
		unsupported[name] = true
	}
	var blocked []string
	for _, name := range multipartRequiredNames(spec.RequestBody.Schema) {
		if unsupported[name] {
			blocked = append(blocked, name)
		}
	}
	if len(blocked) > 0 {
		return fmt.Errorf("required multipart field %q has no supported part encoding; ignore the command in an overlay or change the spec", blocked[0])
	}
	if multipartHasFormData(spec.Params) {
		return nil
	}
	if len(spec.RequestBody.UnsupportedFields) > 0 || multipartObjectSchema(spec.RequestBody.Schema) {
		if spec.RequestBody.Required {
			return fmt.Errorf("required multipart body has no supported part encoding; ignore the command in an overlay or change the spec")
		}
		return nil
	}
	return fmt.Errorf("multipart body has no supported part encoding; ignore the command in an overlay or change the spec")
}

func multipartRequiredNames(schema *runtime.SchemaSpec) []string {
	seen := map[string]bool{}
	var walk func(*runtime.SchemaSpec)
	walk = func(s *runtime.SchemaSpec) {
		if s == nil {
			return
		}
		for _, name := range s.Required {
			seen[name] = true
		}
		for _, child := range s.AllOf {
			walk(child)
		}
	}
	walk(schema)
	return slices.Sorted(maps.Keys(seen))
}

func multipartObjectSchema(schema *runtime.SchemaSpec) bool {
	if schema == nil || len(schema.OneOf) > 0 || len(schema.AnyOf) > 0 || (schema.Type != "" && schema.Type != "object") {
		return false
	}
	if len(schema.AllOf) == 0 {
		return schema.Type == "object" || len(schema.Properties) > 0 || schema.AdditionalProperties != nil
	}
	for _, child := range schema.AllOf {
		if !multipartObjectSchema(child) {
			return false
		}
	}
	return true
}

func multipartHasFormData(params []runtime.ParamSpec) bool {
	for _, param := range params {
		if param.In == runtime.InFormData {
			return true
		}
	}
	return false
}

func multipartMediaType(mediaType string) bool {
	parsed, _, err := mime.ParseMediaType(mediaType)
	return err == nil && parsed == "multipart/form-data"
}

// Write renders every collected output.
func (a *App) Write() error {
	mounts := make([]render.ModuleMount, 0, len(a.Modules))
	sources := make([]runtime.SourceProvenance, 0, len(a.Modules))
	for _, m := range a.Modules {
		if err := render.RenderModule(m.Source, m.CLIName, m.Specs, nil); err != nil {
			return err
		}
		mounts = append(mounts, render.ModuleMount{Name: m.Source, Flat: m.Flat})
		sources = append(sources, m.Provenance)
	}
	opts := render.ModulesGenOptions{Sources: sources}
	if a.Skill != nil && a.Skill.Bundle {
		opts.SkillBundle = &render.SkillBundleMount{Root: render.SkillDirName(a.Manifest.CLI.Name)}
	}
	if len(a.Workflows) > 0 {
		opts.Workflows = true
		if err := render.RenderWorkflows(a.Workflows); err != nil {
			return err
		}
	} else if err := render.RemoveWorkflowsPackage(); err != nil {
		return err
	}
	if err := render.RenderModulesGenWithOptions(mounts, opts); err != nil {
		return err
	}
	if a.Skill == nil {
		return render.RemoveSkillBundlePackage()
	}
	if err := render.RenderSkillDirectoryWithInclude(a.Skill.Dir, a.Manifest, a.Skill.Modules, a.Skill.Include); err != nil {
		return err
	}
	if !a.Skill.Bundle {
		return render.RemoveSkillBundlePackage()
	}
	return render.RenderSkillBundlePackage(a.Skill.Dir, a.Manifest.CLI.Name)
}
