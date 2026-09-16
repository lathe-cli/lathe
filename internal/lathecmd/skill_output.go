package lathecmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lathe-cli/lathe/internal/codegen/render"
	"github.com/lathe-cli/lathe/pkg/config"
	"gopkg.in/yaml.v3"
)

type skillFlagOptions struct {
	Root       string
	RootSet    bool
	Include    string
	IncludeSet bool
}

const (
	kitupGoDependency      = "github.com/lathe-cli/kitup/go@v0.1.3"
	kitupGoCobraDependency = "github.com/lathe-cli/kitup/go-cobra@v0.1.3"
)

func resolveSkillOutput(manifestPath string, flags skillFlagOptions) (*config.Manifest, string, render.SkillInclude, error) {
	manifest, rootConfig, include, err := loadCodegenManifest(manifestPath)
	if err != nil {
		if os.IsNotExist(err) && flags.RootSet && flags.Root == "" && (!flags.IncludeSet || flags.Include == "") {
			return &config.Manifest{CLI: config.CLIInfo{CommandPath: config.CommandPathAuto}}, "", render.SkillInclude{}, nil
		}
		return nil, "", render.SkillInclude{}, err
	}

	manifestIncludes := skillIncludeConfigured(include)

	root := "skills"
	if rootConfig != nil {
		root = *rootConfig
	}
	if flags.RootSet {
		root = flags.Root
	}

	if flags.IncludeSet {
		include.Path = flags.Include
	}

	if root == "" {
		if manifest.Skill.Bundle {
			return nil, "", render.SkillInclude{}, fmt.Errorf("skill.bundle requires skill generation")
		}
		if skillIncludeConfigured(include) || flags.RootSet && manifestIncludes {
			return nil, "", render.SkillInclude{}, fmt.Errorf("skill include requires skill generation")
		}
		return manifest, "", render.SkillInclude{}, nil
	}

	skillDir, err := skillOutputDir(root, manifest.CLI.Name)
	if err != nil {
		return nil, "", render.SkillInclude{}, err
	}
	if err := render.ValidateSkillIncludeRoot(root, include.Path); err != nil {
		return nil, "", render.SkillInclude{}, err
	}
	return manifest, skillDir, include, nil
}

func pinSkillBundleDependencies(output io.Writer) error {
	args := []string{"get", kitupGoDependency, kitupGoCobraDependency}
	fmt.Fprintf(output, "go %s\n", strings.Join(args, " "))
	cmd := exec.Command("go", args...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pin skill bundle dependencies: %w", err)
	}
	return nil
}

func skillIncludeConfigured(include render.SkillInclude) bool {
	return include.Path != "" || len(include.Files) > 0
}

func skillFlagsFrom(fs *flag.FlagSet, root, include *string) skillFlagOptions {
	var rootSet, includeSet bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "skill-root":
			rootSet = true
		case "skill-include":
			includeSet = true
		}
	})
	return skillFlagOptions{
		Root:       *root,
		RootSet:    rootSet,
		Include:    *include,
		IncludeSet: includeSet,
	}
}

func loadCodegenManifest(path string) (*config.Manifest, *string, render.SkillInclude, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, render.SkillInclude{}, err
	}
	manifest, err := config.Load(data)
	if err != nil {
		return nil, nil, render.SkillInclude{}, err
	}
	var codegen struct {
		Skill struct {
			Root    *string            `yaml:"root"`
			Include skillIncludeConfig `yaml:"include"`
		} `yaml:"skill"`
	}
	if err := yaml.Unmarshal(data, &codegen); err != nil {
		return nil, nil, render.SkillInclude{}, fmt.Errorf("parse cli.yaml: %w", err)
	}
	return manifest, codegen.Skill.Root, codegen.Skill.Include.SkillInclude, nil
}

type skillIncludeConfig struct {
	render.SkillInclude
}

func (c *skillIncludeConfig) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		c.Path = value.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i < len(value.Content); i += 2 {
			key := value.Content[i]
			val := value.Content[i+1]
			switch key.Value {
			case "path":
				if val.Kind != yaml.ScalarNode {
					return fmt.Errorf("skill.include.path must be a string")
				}
				c.Path = val.Value
			case "files":
				files, err := decodeSkillIncludeFiles(val)
				if err != nil {
					return err
				}
				c.Files = files
			default:
				return fmt.Errorf("unknown skill.include field %q", key.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("skill.include must be a string or mapping")
	}
}

func decodeSkillIncludeFiles(node *yaml.Node) (map[string]render.SkillFileAction, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("skill.include.files must be a mapping")
	}
	files := map[string]render.SkillFileAction{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		if key.Kind != yaml.ScalarNode || val.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("skill.include.files entries must map file paths to actions")
		}
		if _, ok := files[key.Value]; ok {
			return nil, fmt.Errorf("duplicate skill.include.files entry %q", key.Value)
		}
		files[key.Value] = render.SkillFileAction(val.Value)
	}
	return files, nil
}

func skillOutputDir(root string, cliName string) (string, error) {
	clean := filepath.Clean(root)
	if clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("invalid skill root %q", root)
	}
	return filepath.Join(clean, render.SkillDirName(cliName)), nil
}
