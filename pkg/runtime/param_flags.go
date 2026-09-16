package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func bindParamFlag(cmd *cobra.Command, vals map[string]any, p ParamSpec, hasRequestBody bool) {
	key := boundParamKey(p)
	goType := p.GoType
	if p.In == InPath {
		goType = "string"
	}
	switch goType {
	case "int64":
		def, _ := strconv.ParseInt(p.Default, 10, 64)
		vals[key] = cmd.Flags().Int64(p.Flag, def, p.Help)
	case "float64":
		def, _ := strconv.ParseFloat(p.Default, 64)
		vals[key] = cmd.Flags().Float64(p.Flag, def, p.Help)
	case "bool":
		vals[key] = cmd.Flags().Bool(p.Flag, p.Default == "true", p.Help)
	case "[]int64":
		vals[key] = cmd.Flags().Int64Slice(p.Flag, nil, p.Help)
	case "[]float64":
		vals[key] = cmd.Flags().Float64Slice(p.Flag, nil, p.Help)
	case "[]bool":
		vals[key] = cmd.Flags().BoolSlice(p.Flag, nil, p.Help)
	case "[]string":
		vals[key] = cmd.Flags().StringSlice(p.Flag, nil, p.Help)
	default:
		vals[key] = cmd.Flags().String(p.Flag, p.Default, p.Help)
		addSafeInputFlags(cmd, p)
	}
	if (p.Required || p.In == InPath) && p.Default == "" && p.Argument == "" && p.Context == "" && p.In != InBody && (p.In != InVariable || !hasRequestBody) && !isSensitiveStringParam(p) {
		_ = cmd.MarkFlagRequired(p.Flag)
	}
	if p.Deprecated {
		_ = cmd.Flags().MarkDeprecated(p.Flag, "this flag is deprecated")
	}
}

func ValidateParamFlags(params []ParamSpec) error {
	type bindingOwner struct {
		param  int
		target string
	}
	seen := map[string]bindingOwner{}
	for i, param := range params {
		for _, binding := range paramFlagBindings(param) {
			if binding.name == "" {
				return fmt.Errorf("parameter %q has an empty flag name", param.Name)
			}
			if prior, ok := seen[binding.name]; ok {
				if prior.param != i {
					return fmt.Errorf("flag %q is shared by parameters %q and %q", binding.name, params[prior.param].Name, param.Name)
				}
				if prior.target != binding.target {
					return fmt.Errorf("flag %q has conflicting bindings for parameter %q", binding.name, param.Name)
				}
				continue
			}
			seen[binding.name] = bindingOwner{param: i, target: binding.target}
		}
	}
	return nil
}

type paramFlagBinding struct {
	name   string
	target string
}

func paramFlagBindings(param ParamSpec) []paramFlagBinding {
	bindings := []paramFlagBinding{{name: param.Flag, target: param.Flag}}
	for _, alias := range param.Aliases {
		bindings = append(bindings, paramFlagBinding{name: alias, target: param.Flag})
	}
	if !isSensitiveStringParam(param) {
		return bindings
	}
	for _, suffix := range []string{"-env", "-file", "-stdin"} {
		bindings = append(bindings, paramFlagBinding{name: param.Flag + suffix, target: param.Flag + suffix})
	}
	for _, alias := range param.Aliases {
		for _, suffix := range []string{"-env", "-file", "-stdin"} {
			bindings = append(bindings, paramFlagBinding{name: alias + suffix, target: param.Flag + suffix})
		}
	}
	return bindings
}

func addSafeInputFlags(cmd *cobra.Command, p ParamSpec) {
	if !isSensitiveStringParam(p) {
		return
	}
	cmd.Flags().String(p.Flag+"-env", "", "read --"+p.Flag+" from an environment variable")
	cmd.Flags().String(p.Flag+"-file", "", "read --"+p.Flag+" from a file")
	cmd.Flags().Bool(p.Flag+"-stdin", false, "read --"+p.Flag+" from stdin")
}

func resolveSafeInputFlags(cmd *cobra.Command, params []ParamSpec, vals map[string]any) error {
	for _, p := range params {
		if !isSensitiveStringParam(p) {
			continue
		}
		changed := 0
		for _, flag := range []string{p.Flag, p.Flag + "-env", p.Flag + "-file", p.Flag + "-stdin"} {
			if cmd.Flags().Changed(flag) {
				changed++
			}
		}
		if changed == 0 {
			continue
		}
		if changed > 1 {
			return fmt.Errorf("use only one of --%s, --%s-env, --%s-file, or --%s-stdin", p.Flag, p.Flag, p.Flag, p.Flag)
		}
		var value string
		switch {
		case cmd.Flags().Changed(p.Flag):
			continue
		case cmd.Flags().Changed(p.Flag + "-env"):
			name, _ := cmd.Flags().GetString(p.Flag + "-env")
			value = os.Getenv(name)
			if value == "" {
				return fmt.Errorf("environment variable %s is empty", name)
			}
		case cmd.Flags().Changed(p.Flag + "-file"):
			path, _ := cmd.Flags().GetString(p.Flag + "-file")
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value = string(data)
		case cmd.Flags().Changed(p.Flag + "-stdin"):
			data, err := readStdin(cmd.Context(), os.Stdin)
			if err != nil {
				return err
			}
			value = string(data)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("--%s value is empty", p.Flag)
		}
		*vals[boundParamKey(p)].(*string) = value
	}
	return nil
}

func validateRequiredParams(params []ParamSpec, hasRequestBody bool, changed map[string]bool) error {
	for _, p := range params {
		if !requiredFlagParam(p, hasRequestBody) {
			continue
		}
		if !changed[boundParamKey(p)] {
			return WithUsageDetail(fmt.Errorf("required flag(s) \"%s\" not set", p.Flag), "missing required: --"+p.Flag)
		}
	}
	return nil
}

func positionalParams(params []ParamSpec) []ParamSpec {
	out := make([]ParamSpec, 0)
	for _, p := range params {
		if p.Argument != "" {
			out = append(out, p)
		}
	}
	return out
}

func configureParamFlagAliases(cmd *cobra.Command, params []ParamSpec) {
	aliases := map[string]string{}
	for _, p := range params {
		for _, alias := range p.Aliases {
			if alias == "" || alias == p.Flag {
				continue
			}
			aliases[alias] = p.Flag
			if isSensitiveStringParam(p) {
				for _, suffix := range []string{"-env", "-file", "-stdin"} {
					aliases[alias+suffix] = p.Flag + suffix
				}
			}
		}
	}
	if len(aliases) == 0 {
		return
	}
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if normalized := aliases[name]; normalized != "" {
			return pflag.NormalizedName(normalized)
		}
		return pflag.NormalizedName(name)
	})
}

func bindPositionalArgs(cmd *cobra.Command, args []string, params []ParamSpec, changed map[string]bool) error {
	for i, value := range args {
		p := params[i]
		if flagChanged(cmd, p) {
			return fmt.Errorf("parameter %q cannot use both argument %d and --%s", p.Name, i+1, p.Flag)
		}
		if err := cmd.Flags().Set(p.Flag, value); err != nil {
			return fmt.Errorf("parse argument %d for parameter %q: %w", i+1, p.Name, err)
		}
		key := boundParamKey(p)
		changed[key] = true
	}
	return nil
}

func flagChanged(cmd *cobra.Command, p ParamSpec) bool {
	if cmd.Flags().Changed(p.Flag) {
		return true
	}
	if !isSensitiveStringParam(p) {
		return false
	}
	return cmd.Flags().Changed(p.Flag+"-env") || cmd.Flags().Changed(p.Flag+"-file") || cmd.Flags().Changed(p.Flag+"-stdin")
}

func flagChangedOrDefault(cmd *cobra.Command, p ParamSpec) bool {
	return flagChanged(cmd, p) || (p.Default != "" && p.Context == "")
}

func operationChangedFlags(cmd *cobra.Command, params []ParamSpec) map[string]bool {
	changed := make(map[string]bool, len(params))
	for _, p := range params {
		if flagChangedOrDefault(cmd, p) {
			changed[boundParamKey(p)] = true
		}
	}
	return changed
}

func isSensitiveStringParam(p ParamSpec) bool {
	if p.GoType != "string" {
		return false
	}
	if strings.EqualFold(p.Format, "password") {
		return true
	}
	name := sensitiveNameKey(p.Name + " " + p.Flag)
	for _, marker := range []string{"password", "secret", "credential", "apikey", "privatekey", "accesstoken", "refreshtoken", "bearertoken", "authtoken"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return sensitiveNameKey(p.Name) == "token" || sensitiveNameKey(p.Flag) == "token"
}

func sensitiveNameKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
