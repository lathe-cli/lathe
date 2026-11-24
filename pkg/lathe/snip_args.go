package lathe

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/lathe-cli/lathe/pkg/runtime"
)

func splitLeadingRootFlags(root *cobra.Command, args []string) (leadingFlags []string, name string, rest []string, helpRequested bool, err error) {
	return splitFlagsWithSet(root.PersistentFlags(), args)
}

func splitLeadingCommandFlags(cmd *cobra.Command, args []string) (leadingFlags []string, name string, rest []string, helpRequested bool, err error) {
	fs := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	fs.AddFlagSet(cmd.Root().PersistentFlags())
	fs.AddFlagSet(cmd.Flags())
	return splitFlagsWithSet(fs, args)
}

func splitFlagsWithSet(fs *pflag.FlagSet, args []string) (leadingFlags []string, name string, rest []string, helpRequested bool, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return leadingFlags, args[i+1], args[i+2:], helpRequested, nil
			}
			return leadingFlags, "", nil, helpRequested, nil
		}
		if arg == "--help" || arg == "-h" {
			return leadingFlags, "", nil, true, nil
		}
		if strings.HasPrefix(arg, "--") {
			flagName, val, hasEq := strings.Cut(arg[2:], "=")
			if flagName == "help" {
				if !hasEq || val == "true" {
					return leadingFlags, "", nil, true, nil
				}
				continue
			}
			f := fs.Lookup(flagName)
			if f == nil {
				return leadingFlags, "", nil, false, errors.New("unknown flag")
			}
			if hasEq || f.NoOptDefVal != "" {
				leadingFlags = append(leadingFlags, arg)
				continue
			}
			if i+1 >= len(args) {
				return leadingFlags, "", nil, false, errors.New("flag needs an argument")
			}
			leadingFlags = append(leadingFlags, arg, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			sh := arg[1:2]
			if sh == "h" && len(arg) == 2 {
				return leadingFlags, "", nil, true, nil
			}
			f := fs.ShorthandLookup(sh)
			if f == nil {
				return leadingFlags, "", nil, false, errors.New("unknown shorthand flag")
			}
			if len(arg) > 2 {
				if f.NoOptDefVal != "" && arg[2] != '=' {
					return leadingFlags, "", nil, false, errors.New("unknown shorthand flag")
				}
				leadingFlags = append(leadingFlags, arg)
				continue
			}
			if f.NoOptDefVal != "" {
				leadingFlags = append(leadingFlags, arg)
				continue
			}
			if i+1 >= len(args) {
				return leadingFlags, "", nil, false, errors.New("flag needs an argument")
			}
			leadingFlags = append(leadingFlags, arg, args[i+1])
			i++
			continue
		}
		return leadingFlags, arg, args[i+1:], helpRequested, nil
	}
	return leadingFlags, "", nil, helpRequested, nil
}

func validatePlaceholders(tokens []string) (int, error) {
	seen := map[int]bool{}
	maxIdx := 0
	for _, tok := range tokens {
		for i := 0; i < len(tok); i++ {
			if tok[i] != '$' || i+1 >= len(tok) {
				continue
			}
			if tok[i+1] == '$' {
				i++
				continue
			}
			if tok[i+1] < '0' || tok[i+1] > '9' {
				continue
			}
			j := i + 1
			for j < len(tok) && tok[j] >= '0' && tok[j] <= '9' {
				j++
			}
			digits := tok[i+1 : j]
			if digits[0] == '0' {
				return 0, errors.New("snip placeholders start at $1. Write $$ for a literal '$'")
			}
			idx, err := strconv.Atoi(digits)
			if err != nil || idx <= 0 {
				return 0, errors.New("invalid snip placeholder index")
			}
			seen[idx] = true
			if idx > maxIdx {
				maxIdx = idx
			}
			i = j - 1
		}
	}
	for k := 1; k <= maxIdx; k++ {
		if !seen[k] {
			return 0, fmt.Errorf("placeholder $%d requires $1 through $%d. Write $$ for a literal '$'", maxIdx, maxIdx)
		}
	}
	return maxIdx, nil
}

func expandSnipArgs(errCmd *cobra.Command, savedTokens, leadingFlags, invokeArgs []string) ([]string, error) {
	maxIdx, err := validatePlaceholders(savedTokens)
	if err != nil {
		return nil, runtime.UsageError(errCmd, runtime.WithUsageDetail(err, err.Error()))
	}
	if hasHelpFlag(invokeArgs) {
		expanded := make([]string, len(savedTokens))
		for i, tok := range savedTokens {
			expanded[i] = replacePlaceholdersWithDummy(tok)
		}
		return mergeSnipArgSlices(expanded, leadingFlags, invokeArgs), nil
	}

	values := invokeArgs
	if maxIdx > 0 {
		if len(values) > 0 && values[0] == "--" {
			values = values[1:]
			if len(values) < maxIdx {
				return nil, missingPlaceholderError(errCmd, maxIdx)
			}
		} else {
			if len(values) < maxIdx {
				return nil, missingPlaceholderError(errCmd, maxIdx)
			}
			for _, v := range values[:maxIdx] {
				if looksLikeFlag(v) {
					return nil, missingPlaceholderError(errCmd, maxIdx)
				}
			}
		}
	}
	posArgs := values[:maxIdx]
	trailing := values[maxIdx:]

	expanded := make([]string, len(savedTokens))
	for i, tok := range savedTokens {
		expanded[i] = applyPlaceholders(tok, posArgs)
	}
	return mergeSnipArgSlices(expanded, leadingFlags, trailing), nil
}

func missingPlaceholderError(cmd *cobra.Command, maxIdx int) error {
	detail := "snip requires 1 argument ($1) before flags"
	if maxIdx > 1 {
		detail = fmt.Sprintf("snip requires %d arguments ($1..$%d) before flags", maxIdx, maxIdx)
	}
	return runtime.UsageError(cmd, runtime.WithUsageDetail(errors.New("missing snip arguments"), detail))
}

func mergeSnipArgSlices(saved, leadingFlags, trailing []string) []string {
	out := make([]string, 0, len(saved)+len(leadingFlags)+len(trailing))
	dashIdx := slices.Index(saved, "--")
	if dashIdx >= 0 {
		out = append(out, saved[:dashIdx]...)
		out = append(out, leadingFlags...)
		out = append(out, saved[dashIdx:]...)
		out = append(out, trailing...)
		return out
	}
	out = append(out, saved...)
	out = append(out, leadingFlags...)
	out = append(out, trailing...)
	return out
}

func applyPlaceholders(tok string, args []string) string {
	return substitutePlaceholders(tok, func(idx int) (string, bool) {
		if idx >= 1 && idx <= len(args) {
			return args[idx-1], true
		}
		return "", false
	})
}

func replacePlaceholdersWithDummy(tok string) string {
	return substitutePlaceholders(tok, func(idx int) (string, bool) {
		return "x", idx >= 1
	})
}

func substitutePlaceholders(tok string, resolve func(idx int) (string, bool)) string {
	var b strings.Builder
	b.Grow(len(tok))
	for i := 0; i < len(tok); i++ {
		if tok[i] != '$' || i+1 >= len(tok) {
			b.WriteByte(tok[i])
			continue
		}
		if tok[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		if tok[i+1] >= '1' && tok[i+1] <= '9' {
			j := i + 1
			for j < len(tok) && tok[j] >= '0' && tok[j] <= '9' {
				j++
			}
			if idx, err := strconv.Atoi(tok[i+1 : j]); err == nil {
				if replacement, ok := resolve(idx); ok {
					b.WriteString(replacement)
					i = j - 1
					continue
				}
			}
		}
		b.WriteByte(tok[i])
	}
	return b.String()
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--help" || arg == "-h" || arg == "--help=true" {
			return true
		}
	}
	return false
}

func looksLikeFlag(arg string) bool {
	if arg == "" || arg == "-" {
		return false
	}
	if strings.HasPrefix(arg, "--") {
		return true
	}
	if arg[0] != '-' {
		return false
	}
	if (arg[1] >= '0' && arg[1] <= '9') || arg[1] == '.' {
		return false
	}
	return true
}

func splitSnipArgs(input string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inToken := false
	inSingle := false
	inDouble := false

	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(ch)
			}
		case inDouble:
			if ch == '"' {
				inDouble = false
				continue
			}
			if ch == '\\' {
				if i+1 >= len(input) {
					return nil, errors.New("snip command has a trailing backslash escape")
				}
				next := input[i+1]
				if next == '"' || next == '\\' {
					cur.WriteByte(next)
					i++
					continue
				}
			}
			cur.WriteByte(ch)
		default:
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
				if inToken {
					tokens = append(tokens, cur.String())
					cur.Reset()
					inToken = false
				}
				continue
			}
			if ch == '\'' {
				inSingle = true
				inToken = true
				continue
			}
			if ch == '"' {
				inDouble = true
				inToken = true
				continue
			}
			if ch == '\\' {
				if i+1 >= len(input) {
					return nil, errors.New("snip command has a trailing backslash escape")
				}
				cur.WriteByte(input[i+1])
				inToken = true
				i++
				continue
			}
			cur.WriteByte(ch)
			inToken = true
		}
	}
	if inSingle || inDouble {
		return nil, errors.New("snip command has an unclosed quote")
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

func formatSnipCommand(tokens []string) string {
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		parts[i] = quoteSnipToken(tok)
	}
	return strings.Join(parts, " ")
}

func quoteSnipToken(tok string) string {
	if tok == "" {
		return "''"
	}
	needsQuote := false
	for i := 0; i < len(tok); i++ {
		switch tok[i] {
		case ' ', '\t', '\n', '\r', '\'', '"', '\\':
			needsQuote = true
		}
	}
	if !needsQuote {
		return tok
	}
	if !strings.ContainsRune(tok, '\'') {
		return "'" + tok + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(tok); i++ {
		if tok[i] == '"' || tok[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(tok[i])
	}
	b.WriteByte('"')
	return b.String()
}
