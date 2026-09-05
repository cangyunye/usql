package completer

import (
	"sort"
	"strings"
	"unicode"
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

// completeFuzzy returns the suffixes of options that fuzzily match text,
// best matches first. Candidates that start with the whole pattern rank
// first, then fuzzy score, then length. Case handling mirrors
// CompleteFromList: when text starts with a lower-case letter, suffixes are
// lower-cased.
func completeFuzzy(text []rune, options ...string) [][]rune {
	if len(options) == 0 {
		return nil
	}
	pattern := string(text)
	lowerPattern := strings.ToLower(pattern)
	type match struct {
		option string
		score  int
		prefix bool
	}
	matches := make([]match, 0, len(options))
	for _, o := range options {
		if s := fuzzyScore(pattern, o); s >= 0 {
			matches = append(matches, match{o, s, strings.HasPrefix(strings.ToLower(o), lowerPattern)})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].prefix != matches[j].prefix {
			return matches[i].prefix
		}
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return len(matches[i].option) < len(matches[j].option)
	})
	lower := len(text) > 0 && unicode.IsLower(text[0])
	result := make([][]rune, 0, len(matches))
	for _, m := range matches {
		suffix := m.option[len(pattern):]
		if lower {
			suffix = strings.ToLower(suffix)
		}
		result = append(result, []rune(suffix))
	}
	return result
}
