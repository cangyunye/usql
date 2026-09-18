package completer

import (
	"strings"

	"github.com/xo/usql/rline"
)

// metaArgFunc completes the argIndex-th argument (0-based) of a meta
// command; words are the words before the cursor word in reverse order
// (words[0] is the last typed argument, the command itself is last).
type metaArgFunc func(c *completer, argIndex int, words []string, text []rune) []rline.Cand

// metaArgPolicy is the declarative completion policy for meta-command
// arguments: one entry per argument position; a nil entry or a position past
// the table completes nothing — never SQL keywords. A command's policy is
// found by exact name first, then by the longest registered prefix, so the
// display variants (\dt+, \dtS, \dtS+) share the base command's policy.
var metaArgPolicy = map[string][]metaArgFunc{
	// file arguments
	"cd":               {(*completer).metaFiles},
	"e":                {(*completer).metaFiles},
	"edit":             {(*completer).metaFiles},
	"g":                {(*completer).metaFiles},
	"gx":               {(*completer).metaFiles},
	"i":                {(*completer).metaFiles},
	"include":          {(*completer).metaFiles},
	"ir":               {(*completer).metaFiles},
	"include_relative": {(*completer).metaFiles},
	"o":                {(*completer).metaFiles},
	"out":              {(*completer).metaFiles},
	"s":                {(*completer).metaFiles},
	"w":                {(*completer).metaFiles},
	"write":            {(*completer).metaFiles},
	// connection names
	"c":       {(*completer).metaConns},
	"connect": {(*completer).metaConns},
	"copy":    {(*completer).metaConns, nil},
	// aliases
	"alias": {(*completer).metaAliases},
	// object listings (\dtS+ and friends resolve to their base command)
	"dt": {metaTables(listableTables)},
	"dv": {metaTables(listableViews)},
	"dm": {metaTables(listableMatviews)},
	"da": {metaFunctions([]string{"AGGREGATE"})},
	"df": {metaFunctions(nil)},
	"di": {(*completer).metaIndexes},
	"ds": {(*completer).metaSequences},
	"dn": {(*completer).metaSchemas},
	"d":  {(*completer).metaSelectables},
	"dp": {(*completer).metaSelectables},
	"l":  {(*completer).metaCatalogs},
	"lo": {(*completer).metaCatalogs},
	// \pset: setting name, then setting-specific values
	"pset": {(*completer).metaPset, (*completer).metaPsetValue},
	// \?: section names
	"?": {func(c *completer, _ int, _ []string, text []rune) []rline.Cand {
		return completeFuzzyFull(string(text), rline.Cands("commands", "options", "variables"))
	}},
}

// listable type sets for the table listings.
var (
	listableTables   = []string{"TABLE", "BASE TABLE", "SYSTEM TABLE", "SYNONYM", "LOCAL TEMPORARY", "GLOBAL TEMPORARY"}
	listableViews    = []string{"VIEW", "SYSTEM VIEW"}
	listableMatviews = []string{"MATERIALIZED VIEW"}
)

// metaPolicyFor resolves a command word (with backslash) to its policy.
// Display variants (\dtS+) reduce to the base command by stripping their
// trailing S/+, and only an exact name then matches — a general prefix match
// would wrongly give \encoding the \e file policy.
func metaPolicyFor(cmdWord string) []metaArgFunc {
	cmd := strings.TrimPrefix(cmdWord, "\\")
	cmd = strings.TrimRight(cmd, "S+")
	if p, ok := metaArgPolicy[strings.ToLower(cmd)]; ok {
		return p
	}
	return nil
}

// completeMetaArg applies the policy for the command's argIndex-th argument.
func (c completer) completeMetaArg(cmdWord string, argIndex int, words []string, text []rune) []rline.Cand {
	pol := metaPolicyFor(cmdWord)
	if argIndex >= len(pol) || pol[argIndex] == nil {
		return nil
	}
	return pol[argIndex](&c, argIndex, words, text)
}

func (c *completer) metaFiles(_ int, _ []string, text []rune) []rline.Cand {
	return completeFuzzyFull(string(text), completeFromFiles(text))
}

func (c *completer) metaConns(_ int, _ []string, text []rune) []rline.Cand {
	return completeFuzzyFull(string(text), rline.Cands(c.connStrings...))
}

func (c *completer) metaAliases(_ int, _ []string, text []rune) []rline.Cand {
	return completeFuzzyFull(string(text), rline.Cands(c.aliasNames...))
}

// metaTables completes a table listing of the given types.
func metaTables(types []string) metaArgFunc {
	return func(c *completer, _ int, _ []string, text []rune) []rline.Cand {
		return c.completeWithTables(text, types)
	}
}

// metaFunctions completes a function listing, optionally filtered to types.
func metaFunctions(types []string) metaArgFunc {
	return func(c *completer, _ int, _ []string, text []rune) []rline.Cand {
		return c.completeWithFunctions(text, types)
	}
}

func (c *completer) metaIndexes(_ int, _ []string, text []rune) []rline.Cand {
	return c.completeWithIndexes(text)
}

func (c *completer) metaSchemas(_ int, _ []string, text []rune) []rline.Cand {
	return c.completeWithSchemas(text)
}

func (c *completer) metaSequences(_ int, _ []string, text []rune) []rline.Cand {
	return c.completeWithSequences(text)
}

func (c *completer) metaSelectables(_ int, _ []string, text []rune) []rline.Cand {
	return c.completeWithSelectablesFull(text)
}

func (c *completer) metaCatalogs(_ int, _ []string, text []rune) []rline.Cand {
	return c.completeWithCatalogs(text)
}

// psetSettings are the \pset setting names.
var psetSettings = []string{`border`, `columns`, `expanded`, `fieldsep`, `fieldsep_zero`,
	`footer`, `format`, `linestyle`, `null`, `numericlocale`, `pager`, `pager_min_lines`,
	`recordsep`, `recordsep_zero`, `tableattr`, `title`, `tuples_only`,
	`unicode_border_linestyle`, `unicode_column_linestyle`, `unicode_header_linestyle`}

// psetValues are the per-setting values.
var psetValues = map[string][]string{
	"expanded":                 {"auto", "on", "off"},
	"pager":                    {"always", "on", "off"},
	"fieldsep_zero":            {"on", "off"},
	"footer":                   {"on", "off"},
	"numericlocale":            {"on", "off"},
	"recordsep_zero":           {"on", "off"},
	"tuples_only":              {"on", "off"},
	"format":                   {"unaligned", "aligned", "wrapped", "html", "asciidoc", "latex", "latex-longtable", "troff-ms", "csv", "json", "vertical"},
	"linestyle":                {"ascii", "old-ascii", "unicode"},
	"unicode_border_linestyle": {"single", "double"},
	"unicode_column_linestyle": {"single", "double"},
	"unicode_header_linestyle": {"single", "double"},
}

func (c *completer) metaPset(argIndex int, _ []string, text []rune) []rline.Cand {
	return completeFuzzyFull(string(text), rline.Cands(psetSettings...))
}

func (c *completer) metaPsetValue(_ int, words []string, text []rune) []rline.Cand {
	if len(words) == 0 {
		return nil
	}
	vals, ok := psetValues[strings.ToLower(words[0])]
	if !ok {
		return nil
	}
	return completeFuzzyFull(string(text), rline.Cands(vals...))
}
