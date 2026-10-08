package completer

import (
	"sort"
	"strings"
	"unicode"

	"github.com/xo/usql/rline"
)

// prefixBonus is added when the candidate starts with the whole pattern, so
// exact prefixes rank above fuzzy subsequence matches.
const prefixBonus = 4

// fuzzyScore scores candidate against pattern as a case-insensitive
// subsequence match, returning -1 when candidate does not contain all pattern
// characters in order. Higher scores are better: matches at word boundaries,
// consecutive runs and full-prefix matches are rewarded.
func fuzzyScore(pattern, candidate string) int {
	if pattern == "" {
		return 0
	}
	p, c := strings.ToLower(pattern), strings.ToLower(candidate)
	score := subsequenceScore(p, c)
	if score >= 0 && strings.HasPrefix(c, p) {
		score += prefixBonus
	}
	return score
}

// subsequenceScore greedily matches the pattern characters in order against
// the candidate, rewarding boundary and consecutive matches.
func subsequenceScore(p, c string) int {
	score := 0
	last := -2
	pi := 0
	for ci := 0; ci < len(c) && pi < len(p); ci++ {
		if c[ci] != p[pi] {
			continue
		}
		score++
		if ci == 0 || isBoundaryByte(c[ci-1]) {
			score += 2
		}
		if ci == last+1 {
			score++
		}
		last = ci
		pi++
	}
	if pi < len(p) {
		return -1
	}
	return score
}

// isBoundaryByte reports whether b precedes a word boundary.
func isBoundaryByte(b byte) bool {
	switch b {
	case '_', '.', '-', ' ', '/', '(', ':':
		return true
	}
	return false
}

// mirrorsTypedCase reports whether a candidate mirrors the typed pattern's
// case. Only relation and namespace candidates must keep their stored case:
// on case-sensitive engines (MySQL on Linux with lower_case_table_names=0)
// the stored table/database name is the only one that resolves, and file
// paths are case-sensitive too. Keywords, functions and columns mirror the
// typing habit — names in those classes are case-insensitive on every
// supported engine.
func mirrorsTypedCase(kind string) bool {
	switch kind {
	case "", "keyword", "function", "column":
		return true
	}
	return false
}

// completeFuzzyFull returns the options that fuzzily match pattern as full
// words — used by the replace-style context path, where candidates replace
// the word at the cursor entirely (e.g. schema.table). Prefix matches rank
// first, then fuzzy score, then length. Candidates that mirror typed case
// (see mirrorsTypedCase) are lower-cased when the pattern starts with a
// lower-case letter.
func completeFuzzyFull(pattern string, options []rline.Cand) []rline.Cand {
	lowerPattern := strings.ToLower(pattern)
	type match struct {
		cand   rline.Cand
		score  int
		prefix bool
	}
	matches := make([]match, 0, len(options))
	for _, o := range options {
		if s := fuzzyScore(pattern, o.Text); s >= 0 {
			matches = append(matches, match{o, s, strings.HasPrefix(strings.ToLower(o.Text), lowerPattern)})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].prefix != matches[j].prefix {
			return matches[i].prefix
		}
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return len(matches[i].cand.Text) < len(matches[j].cand.Text)
	})
	lower := len(pattern) > 0 && unicode.IsLower(rune(pattern[0]))
	result := make([]rline.Cand, 0, len(matches))
	for _, m := range matches {
		if lower && mirrorsTypedCase(m.cand.Kind) {
			m.cand.Text = strings.ToLower(m.cand.Text)
		}
		result = append(result, m.cand)
	}
	return result
}

// completePrefixFull returns the options matched from the start — anchored
// matching for the context path, where candidates replace the word at the
// cursor. An option matches when it starts with the pattern (case
// insensitive), or — for a dotless pattern, so bare table names complete
// against qualified candidates — when its last dot-separated segment does
// ("film" matches "public.film"; "db" does not match "foo_db_bar").
// Results keep a stable shortest-first order, and candidates that mirror
// typed case (see mirrorsTypedCase) are lower-cased when the pattern starts
// with a lower-case letter.
func completePrefixFull(pattern string, options []rline.Cand) []rline.Cand {
	p := strings.ToLower(pattern)
	dotted := strings.Contains(pattern, ".")
	var matches []rline.Cand
	for _, o := range options {
		low := strings.ToLower(o.Text)
		if strings.HasPrefix(low, p) {
			matches = append(matches, o)
			continue
		}
		// bare word: also match the object segment of qualified candidates
		if !dotted {
			if i := strings.LastIndexByte(low, '.'); i >= 0 && strings.HasPrefix(low[i+1:], p) {
				matches = append(matches, o)
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return len(matches[i].Text) < len(matches[j].Text)
	})
	lower := len(pattern) > 0 && unicode.IsLower(rune(pattern[0]))
	result := make([]rline.Cand, 0, len(matches))
	for _, m := range matches {
		if lower && mirrorsTypedCase(m.Kind) {
			m.Text = strings.ToLower(m.Text)
		}
		result = append(result, m)
	}
	return result
}
