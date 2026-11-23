package runtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func TestUsageHintIdentifierSafety(t *testing.T) {
	for _, tc := range []struct{ name, input, clean string }{
		{"terminal escape", "\x1b]52;c;cHJvYmU=\a", " ]52;c;cHJvYmU= "},
		{"bidi override", "\u202e", " "},
		{"whitespace", "\n\t", " "},
		{"code delimiter", "`", "'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := func(prefix string) string { return prefix + tc.input + "x" }
			clean := func(prefix string) string { return prefix + tc.clean + "x" }
			root := &cobra.Command{Use: name("cli")}
			group := &cobra.Command{Use: name("group")}
			leaf := &cobra.Command{Use: name("create")}
			root.AddCommand(group)
			group.AddCommand(leaf)
			commands := &cobra.Command{Use: "commands"}
			commands.AddCommand(&cobra.Command{Use: "show"})
			root.AddCommand(commands)
			usage := UsageError(leaf, errors.New("invalid input"))
			body := bodyUsageError(leaf, staticBodySchemaError("invalid body"), name("file"), name("set"), name("str"))
			for _, test := range []struct {
				err  *LatheError
				want string
			}{
				{usage, fmt.Sprintf("run `%s %s %s --help` and correct the arguments", clean("cli"), clean("group"), clean("create"))},
				{body, fmt.Sprintf("run `%s commands show %s %s --json` to inspect body.schema, then correct --%s, --%s, or --%s", clean("cli"), clean("group"), clean("create"), clean("set"), clean("str"), clean("file"))},
			} {
				if test.err.Hint != test.want {
					t.Errorf("hint = %q, want %q", test.err.Hint, test.want)
				}
				assertCleanHint(t, test.err.Hint)
				for _, format := range []string{"json", "yaml", "table"} {
					var out strings.Builder
					if FormatError(test.err, format, &out) != ExitUsage {
						t.Fatalf("expected exit 2 in %s: %q", format, out.String())
					}
					if strings.ContainsAny(out.String(), "\x1b\a\u202e") {
						t.Errorf("unsafe %s output: %q", format, out.String())
					}
				}
			}
		})
	}
}

func TestUsageHintBounds(t *testing.T) {
	root := &cobra.Command{Use: strings.Repeat("long", 100)}
	commands := &cobra.Command{Use: "commands"}
	commands.AddCommand(&cobra.Command{Use: "show"})
	root.AddCommand(commands)
	leaf := &cobra.Command{Use: "create"}
	root.AddCommand(leaf)
	for _, err := range []*LatheError{
		UsageError(leaf, errors.New("invalid input")),
		bodyUsageError(leaf, staticBodySchemaError("invalid body"), "file", "set", "set-str"),
		bodyUsageError(nil, NewError(CodeUsage, ExitUsage, "invalid body", "inspect\u202e\n"+strings.Repeat("schema", 100), nil), "file", "set", "set-str"),
	} {
		assertCleanHint(t, err.Hint)
	}
}

func assertCleanHint(t *testing.T, hint string) {
	t.Helper()
	if strings.Contains(hint, "%!(") {
		t.Errorf("invalid hint formatting: %q", hint)
	}
	if utf8.RuneCountInString(hint) > 240 || hint != strings.Join(strings.Fields(hint), " ") {
		t.Errorf("unbounded or multiline hint: %q", hint)
	}
	for _, r := range hint {
		if !unicode.IsGraphic(r) {
			t.Errorf("non-graphic rune in hint: %q", hint)
			break
		}
	}
}

func TestBodyUsageLiteralDetail(t *testing.T) {
	const detail = "request body must contain one valid JSON value"
	le := bodyUsageError(nil, bodyUsageDetail(detail), "file", "set", "set-str")
	if le.Detail != detail || strings.Contains(le.Hint, "%!(") {
		t.Fatalf("invalid literal diagnostic: detail=%q hint=%q", le.Detail, le.Hint)
	}
}

func TestHiddenBodyHintWithoutCatalogFlag(t *testing.T) {
	root := &cobra.Command{Use: "cli"}
	commands := &cobra.Command{Use: "commands"}
	commands.AddCommand(&cobra.Command{Use: "show"})
	root.AddCommand(commands)
	leaf := &cobra.Command{Use: "create", Hidden: true}
	root.AddCommand(leaf)
	le := bodyUsageError(leaf, staticBodySchemaError("invalid body"), "file", "set", "set-str")
	if strings.Contains(le.Hint, "--include-hidden") {
		t.Fatalf("hint advertises an unavailable flag: %q", le.Hint)
	}
}
