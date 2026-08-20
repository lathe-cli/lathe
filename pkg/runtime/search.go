package runtime

import (
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

func SearchCatalog(root *cobra.Command, query string, opts SearchOptions) []SearchResult {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	tokens := searchTokens(query)
	if len(tokens) == 0 {
		return []SearchResult{}
	}
	stems := stemTokens(tokens)
	fullQuery := strings.ToLower(query)
	normalizedQuery := normalizeSearchText(query)
	results := make([]SearchResult, 0)
	for _, cmd := range BuildCatalog(root, opts.CatalogOptions).Commands {
		view := newSearchView(cmd)
		score, ok := scoreCatalogCommand(view, tokens, stems, fullQuery, normalizedQuery)
		if !ok {
			continue
		}
		results = append(results, SearchResult{Score: score, Command: cmd})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return slices.Compare(results[i].Command.Path, results[j].Command.Path) < 0
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// matchKind orders the lexical layers from most to least precise. A query token
// scores against a field through its best layer only, so a stem match can never
// outrank a real token match on the same field.
type matchKind int

const (
	matchNone matchKind = iota
	matchStemPrefix
	matchStemExact
	matchPrefix
	matchExact
)

const minPrefixMatchLen = 2

const minStemPrefixMatchLen = 3

type searchField struct {
	tokens []string
	stems  []string
	weight int
}

type searchView struct {
	identity    []searchField
	descriptive []searchField
	fullPath    string
	use         string
	operationID string
	shortcuts   []string
}

// newSearchView precomputes the normalized tokens and stems of every searchable
// field once per command, so scoring a query is plain slice comparison.
func newSearchView(cmd CatalogCommand) searchView {
	view := searchView{
		fullPath:    strings.ToLower(strings.Join(cmd.Path, " ")),
		use:         strings.ToLower(cmd.Use),
		operationID: strings.ToLower(cmd.OperationID),
	}
	for _, shortcut := range cmd.Shortcuts {
		view.shortcuts = append(view.shortcuts, strings.ToLower(shortcut.Use))
	}

	view.identity = appendSearchField(view.identity, cmd.OperationID, weightOperationID)
	view.identity = appendSearchField(view.identity, cmd.Use, weightUse)
	for _, alias := range cmd.Aliases {
		view.identity = appendSearchField(view.identity, alias, weightAlias)
	}
	for _, shortcut := range cmd.Shortcuts {
		view.identity = appendSearchField(view.identity, shortcut.Use, weightAlias)
	}
	view.identity = appendSearchField(view.identity, strings.Join(cmd.Path, " "), weightPath)
	view.identity = appendSearchField(view.identity, cmd.HTTP.PathTemplate, weightPathTemplate)

	view.descriptive = appendSearchField(view.descriptive, cmd.Summary, weightText)
	view.descriptive = appendSearchField(view.descriptive, cmd.Description, weightText)
	view.descriptive = appendSearchField(view.descriptive, cmd.Service, weightText)
	view.descriptive = appendSearchField(view.descriptive, cmd.Group, weightText)
	view.descriptive = appendSearchField(view.descriptive, cmd.HTTP.Method, weightText)
	for _, flag := range cmd.Flags {
		view.descriptive = appendSearchField(view.descriptive, flag.Flag, weightFlag)
		view.descriptive = appendSearchField(view.descriptive, flag.Name, weightFlag)
		view.descriptive = appendSearchField(view.descriptive, flag.Help, weightFlagHelp)
	}
	return view
}

const (
	weightOperationID  = 90
	weightUse          = 80
	weightAlias        = 80
	weightPath         = 60
	weightPathTemplate = 45
	weightText         = 30
	weightFlag         = 25
	weightFlagHelp     = 10
)

func appendSearchField(fields []searchField, text string, weight int) []searchField {
	tokens := searchTokens(text)
	if len(tokens) == 0 {
		return fields
	}
	return append(fields, searchField{tokens: tokens, stems: stemTokens(tokens), weight: weight})
}

func scoreCatalogCommand(view searchView, tokens []string, stems []string, fullQuery string, normalizedQuery string) (int, bool) {
	score := 0
	matches := 0
	identity := false
	for i, token := range tokens {
		best, viaIdentity := 0, false
		for _, field := range view.identity {
			if value := scoreField(field, token, stems[i]); value > best {
				best, viaIdentity = value, true
			}
		}
		for _, field := range view.descriptive {
			if value := scoreField(field, token, stems[i]); value > best {
				best, viaIdentity = value, false
			}
		}
		if best == 0 {
			continue
		}
		matches++
		if viaIdentity {
			identity = true
		}
		score += best
	}
	// A single identifying hit is enough to surface a command; a description or
	// flag hit is too weak on its own and needs corroboration from a second token.
	if matches == 0 || (!identity && matches < 2) {
		return 0, false
	}
	// Covering more of the query beats scoring higher on part of it: "creating
	// tokens" must rank create-token above list-tokens, which only matches the
	// noun. Unmatched noise tokens scale every candidate equally, so they shift
	// no ordering.
	score = score * matches / len(tokens)
	if fullQuery == view.fullPath || fullQuery == view.operationID || fullQuery == view.use ||
		normalizedQuery == normalizeSearchText(view.fullPath) ||
		normalizedQuery == normalizeSearchText(view.operationID) ||
		normalizedQuery == normalizeSearchText(view.use) ||
		slices.Contains(view.shortcuts, fullQuery) ||
		slices.Contains(view.shortcuts, normalizedQuery) {
		score += 100
	}
	return score, true
}

func scoreField(field searchField, token string, stem string) int {
	best := matchNone
	for i, candidate := range field.tokens {
		kind := matchToken(candidate, field.stems[i], token, stem)
		if kind > best {
			best = kind
		}
		if best == matchExact {
			break
		}
	}
	return scaleWeight(field.weight, best)
}

func matchToken(candidate string, candidateStem string, token string, stem string) matchKind {
	if candidate == token {
		return matchExact
	}
	if len(token) >= minPrefixMatchLen && strings.HasPrefix(candidate, token) {
		return matchPrefix
	}
	if candidateStem == stem {
		return matchStemExact
	}
	if len(stem) >= minStemPrefixMatchLen && strings.HasPrefix(candidateStem, stem) {
		return matchStemPrefix
	}
	return matchNone
}

func scaleWeight(weight int, kind matchKind) int {
	switch kind {
	case matchExact:
		return weight
	case matchPrefix:
		return weight * 75 / 100
	case matchStemExact:
		return weight * 45 / 100
	case matchStemPrefix:
		return weight * 28 / 100
	default:
		return 0
	}
}

func searchTokens(query string) []string {
	return strings.Fields(normalizeSearchText(query))
}

func stemTokens(tokens []string) []string {
	stems := make([]string, len(tokens))
	for i, token := range tokens {
		stems[i] = stemToken(token)
	}
	return stems
}

// stemToken strips a small set of English inflections so queries like "revoking"
// or "runs" can still reach "revoke-token" and "run-job". It is deliberately not
// a full Porter stemmer: stems only feed the two lowest scoring layers, so a
// missed inflection costs little while an over-stemmed token would pull
// unrelated commands into every result set. Length guards exist for that reason
// ("ping" must not become "p", "string" must not become "str").
func stemToken(token string) string {
	if len(token) < 4 {
		return token
	}
	switch {
	case strings.HasSuffix(token, "ss"), strings.HasSuffix(token, "us"), strings.HasSuffix(token, "is"):
		return token
	case strings.HasSuffix(token, "ies") && len(token) >= 5:
		return token[:len(token)-3] + "y"
	case strings.HasSuffix(token, "sses"), strings.HasSuffix(token, "xes"),
		strings.HasSuffix(token, "zes"), strings.HasSuffix(token, "ches"), strings.HasSuffix(token, "shes"):
		return token[:len(token)-2]
	case strings.HasSuffix(token, "s"):
		return token[:len(token)-1]
	case strings.HasSuffix(token, "ing") && len(token) >= 7:
		return undoubleFinalConsonant(token[:len(token)-3])
	case strings.HasSuffix(token, "ed") && len(token) >= 6:
		return undoubleFinalConsonant(token[:len(token)-2])
	}
	return token
}

func undoubleFinalConsonant(stem string) string {
	if len(stem) < 3 {
		return stem
	}
	last := stem[len(stem)-1]
	if stem[len(stem)-2] != last || strings.ContainsRune("aeioulsz", rune(last)) {
		return stem
	}
	return stem[:len(stem)-1]
}

func normalizeSearchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if b.Len() > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
			prev = r
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		prev = 0
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
