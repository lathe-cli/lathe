package diagnostic

import (
	"strings"
	"unicode"
)

func Identifier(value string) string {
	value = strings.Map(func(r rune) rune {
		if !unicode.IsGraphic(r) {
			return ' '
		}
		if r == '`' {
			return '\''
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}
