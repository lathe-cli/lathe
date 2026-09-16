package render

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type SkillFileAction string

const (
	SkillFileAppend  SkillFileAction = "append"
	SkillFileCreate  SkillFileAction = "create"
	SkillFileReplace SkillFileAction = "replace"
	SkillFileOmit    SkillFileAction = "omit"
)

type SkillInclude struct {
	Path  string
	Files map[string]SkillFileAction
}

func ValidateSkillIncludeRoot(skillRoot, includeRoot string) error {
	if includeRoot == "" {
		return nil
	}
	info, err := os.Stat(includeRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill include root %q does not exist", includeRoot)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("skill include root %q is not a directory", includeRoot)
	}
	if skillRoot == "" {
		return nil
	}
	absRoot, err := filepath.Abs(skillRoot)
	if err != nil {
		return err
	}
	absInclude, err := filepath.Abs(includeRoot)
	if err != nil {
		return err
	}
	if pathContains(absRoot, absInclude) || pathContains(absInclude, absRoot) {
		return fmt.Errorf("skill include root %q must be outside skill root %q", includeRoot, skillRoot)
	}
	return nil
}

func normalizeSkillInclude(include SkillInclude) (SkillInclude, error) {
	if len(include.Files) == 0 {
		return include, nil
	}
	out := SkillInclude{Path: include.Path, Files: map[string]SkillFileAction{}}
	for raw, action := range include.Files {
		rel, err := normalizeSkillRel(raw)
		if err != nil {
			return SkillInclude{}, err
		}
		if _, ok := out.Files[rel]; ok {
			return SkillInclude{}, fmt.Errorf("duplicate skill include policy for %q", rel)
		}
		out.Files[rel] = action
	}
	return out, nil
}

func normalizeSkillRel(rel string) (string, error) {
	clean := filepath.Clean(rel)
	slash := filepath.ToSlash(clean)
	if slash == "" || slash == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("invalid skill file path %q", rel)
	}
	if slash == skillOwnerFile {
		return "", fmt.Errorf("skill file %q is reserved", rel)
	}
	for _, part := range strings.Split(slash, "/") {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("skill file path %q must not contain dotfile segments", rel)
		}
	}
	if _, err := defaultSkillFileAction(slash); err != nil {
		return "", err
	}
	return slash, nil
}

func defaultSkillFileAction(rel string) (SkillFileAction, error) {
	switch {
	case rel == "SKILL.md":
		return SkillFileAppend, nil
	case strings.HasPrefix(rel, "references/") && strings.HasSuffix(rel, ".md"):
		return SkillFileAppend, nil
	case strings.HasPrefix(rel, "agents/"),
		strings.HasPrefix(rel, "assets/"),
		strings.HasPrefix(rel, "references/"),
		strings.HasPrefix(rel, "scripts/"):
		return SkillFileCreate, nil
	default:
		return "", fmt.Errorf("skill include file %q must target SKILL.md, agents/, assets/, references/, or scripts/", rel)
	}
}

func validateSkillIncludePolicy(include SkillInclude, generated map[string]skillFile) error {
	for rel, action := range include.Files {
		switch action {
		case SkillFileReplace:
			if !canReplaceGeneratedSkillFile(rel) {
				return fmt.Errorf("skill include file %q cannot replace a generated Skill control file", rel)
			}
			if _, ok := generated[rel]; !ok {
				return fmt.Errorf("skill include file %q uses replace but no generated file exists at that path", rel)
			}
		case SkillFileOmit:
			if !canOmitGeneratedSkillFile(rel) {
				return fmt.Errorf("skill include file %q cannot be omitted", rel)
			}
			if _, ok := generated[rel]; !ok {
				return fmt.Errorf("skill include file %q uses omit but no generated file exists at that path", rel)
			}
		case SkillFileAppend:
			if !canAppendSkillFile(rel) {
				return fmt.Errorf("skill include file %q cannot be appended", rel)
			}
		case SkillFileCreate:
			if _, ok := generated[rel]; ok {
				return fmt.Errorf("skill include file %q uses create but a generated file already exists at that path", rel)
			}
		default:
			return fmt.Errorf("skill include file %q has invalid action %q", rel, action)
		}
	}
	return nil
}

func canAppendSkillFile(rel string) bool {
	return rel == "SKILL.md" || strings.HasPrefix(rel, "references/") && strings.HasSuffix(rel, ".md")
}

func canReplaceGeneratedSkillFile(rel string) bool {
	return rel == "SKILL.md" ||
		rel == "agents/openai.yaml" ||
		rel == "references/catalog.md" ||
		strings.HasPrefix(rel, "references/modules/") && strings.HasSuffix(rel, ".md")
}

func canOmitGeneratedSkillFile(rel string) bool {
	return rel == "agents/openai.yaml" ||
		strings.HasPrefix(rel, "references/modules/") && strings.HasSuffix(rel, ".md")
}

func filterOmittedModuleRefs(refs []moduleRef, include SkillInclude) []moduleRef {
	if len(include.Files) == 0 {
		return refs
	}
	out := refs[:0]
	for _, ref := range refs {
		if include.Files["references/modules/"+ref.File] == SkillFileOmit {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func mergeSkillIncludes(files map[string]skillFile, generated map[string]skillFile, include SkillInclude) error {
	includeFiles, err := loadSkillIncludeFiles(include.Path)
	if err != nil {
		return err
	}
	covered := map[string]bool{}
	for rel, action := range include.Files {
		covered[rel] = true
		if err := applySkillIncludeAction(files, generated, includeFiles, rel, action); err != nil {
			return err
		}
	}
	for rel := range includeFiles {
		if covered[rel] {
			continue
		}
		action, err := defaultSkillFileAction(rel)
		if err != nil {
			return err
		}
		if err := applySkillIncludeAction(files, generated, includeFiles, rel, action); err != nil {
			return err
		}
	}
	return nil
}

func loadSkillIncludeFiles(includeRoot string) (map[string]skillFile, error) {
	out := map[string]skillFile{}
	if includeRoot == "" {
		return out, nil
	}
	if err := ValidateSkillIncludeRoot("", includeRoot); err != nil {
		return nil, err
	}
	err := filepath.WalkDir(includeRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rawRel, err := filepath.Rel(includeRoot, path)
		if err != nil {
			return err
		}
		rel, err := normalizeSkillRel(rawRel)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill include file %q is not a regular file", path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, ok := out[rel]; ok {
			return fmt.Errorf("duplicate skill include file %q", rel)
		}
		mode := fs.FileMode(0o644)
		if strings.HasPrefix(rel, "scripts/") && info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		out[rel] = skillFile{body: body, mode: mode}
		return nil
	})
	return out, err
}

func applySkillIncludeAction(files map[string]skillFile, generated map[string]skillFile, includeFiles map[string]skillFile, rel string, action SkillFileAction) error {
	included, hasIncluded := includeFiles[rel]
	switch action {
	case SkillFileOmit:
		if hasIncluded {
			return fmt.Errorf("skill include file %q uses omit but an include file exists at that path", rel)
		}
		delete(files, rel)
		return nil
	case SkillFileReplace:
		if !hasIncluded {
			return fmt.Errorf("skill include file %q uses replace but no include file exists at that path", rel)
		}
		files[rel] = included
		return nil
	case SkillFileAppend:
		if !hasIncluded {
			return fmt.Errorf("skill include file %q uses append but no include file exists at that path", rel)
		}
		existing, ok := files[rel]
		if !ok {
			files[rel] = included
			return nil
		}
		merged := append([]byte(nil), existing.body...)
		if len(merged) > 0 && merged[len(merged)-1] != '\n' {
			merged = append(merged, '\n')
		}
		merged = append(merged, '\n')
		merged = append(merged, included.body...)
		files[rel] = skillFile{body: merged, mode: existing.mode}
		return nil
	case SkillFileCreate:
		if !hasIncluded {
			return fmt.Errorf("skill include file %q uses create but no include file exists at that path", rel)
		}
		if _, ok := generated[rel]; ok {
			return fmt.Errorf("skill include file %q targets an existing generated file", rel)
		}
		if _, ok := files[rel]; ok {
			return fmt.Errorf("skill include file %q targets an existing file", rel)
		}
		files[rel] = included
		return nil
	}
	return fmt.Errorf("skill include file %q has invalid action %q", rel, action)
}

func pathContains(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
