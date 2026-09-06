package completer

import (
	"regexp"
	"strings"

	"github.com/xo/usql/drivers/metadata"
)

// updatableTypes are the table types offered in INSERT INTO and UPDATE
// positions, mirroring completeWithUpdatables.
var updatableTypes = []string{"TABLE", "BASE TABLE", "LOCAL TEMPORARY", "GLOBAL TEMPORARY", "VIEW"}

// booleanKeywords continue a column expression in a condition clause.
var booleanKeywords = []string{
	"AND", "OR", "NOT", "IN", "IS NULL", "IS NOT NULL", "LIKE",
	"BETWEEN", "EXISTS", "CASE", "WHEN", "THEN", "ELSE", "END",
}

// columnClauses are clauses in which an unqualified word is (usually) a
// column.
var columnClauses = map[string]bool{
	"SELECT": true, "WHERE": true, "ON": true, "GROUP": true,
	"ORDER": true, "HAVING": true, "SET": true,
}

// clauseKeywords are extra keyword candidates offered in a clause, on top of
// the columns in scope.
var clauseKeywords = map[string][]string{
	"WHERE":  booleanKeywords,
	"ON":     booleanKeywords,
	"HAVING": booleanKeywords,
}

// scopeRE matches statements that change the session's default scope: a
// MySQL/OceanBase USE, a PostgreSQL search_path assignment, or an Oracle
// CURRENT_SCHEMA change.
var scopeRE = regexp.MustCompile(`(?is)^\s*(use\s|set\s+.*search_path|alter\s+session\s+set\s+current_schema)`)

// ScopeChanged reports whether executing sqlstr can change the session's
// default database/schema, so cached completion metadata must be dropped.
func ScopeChanged(sqlstr string) bool {
	return scopeRE.MatchString(sqlstr)
}

// Invalidate drops all cached completion metadata. The installed completer
// (as wrapped by NewLive) satisfies interface{ Invalidate() }; the handler
// calls it after statements that change the session scope.
func (c *completer) Invalidate() {
	if c.cache != nil {
		c.cache.clear()
	}
}

// WithContextCompletion returns an Option that tries context-aware candidate
// generation — clause scanning, alias resolution and fuzzy ranking — before
// the tail-matching heuristics run. Whenever the statement context does not
// determine a candidate set, completion falls through to the heuristics, so
// existing completions (backslash commands, variables, INSERT INTO ...
// VALUES, ...) are unaffected.
//
// Any beforeComplete hook set by an earlier option (e.g. a driver's own
// completion, as in drivers/metadata/mysql) is preserved and tried after
// this one, so drivers' NewCompleter implementations compose by prepending
// their options before the caller's.
func WithContextCompletion() Option {
	return func(c *completer) {
		// cache metadata queries, so typing-time completion stays responsive
		// even when the catalog is slow (e.g. OceanBase)
		if c.reader != nil {
			c.cache = NewCachedReader(c.reader).(*cachedReader)
			c.reader = c.cache
		}
		prev := c.beforeComplete
		c.beforeComplete = func(previousWords []string, text []rune) [][]rune {
			if result := c.completeWithContext(previousWords, text); result != nil {
				return result
			}
			if prev != nil {
				return prev(previousWords, text)
			}
			return nil
		}
	}
}

// completeWithContext is the beforeComplete-shaped entry point: it rebuilds
// the line up to the cursor from the previous words, parses the clause
// context, and generates candidates from it. It returns nil when the context
// is inconclusive.
func (c completer) completeWithContext(previousWords []string, text []rune) [][]rune {
	line := reconstructLine(previousWords, text)
	return c.completeFromContext(parseContext(line, len(line)))
}

// reconstructLine joins the previous words — which Do provides in reverse
// order — and the word at the cursor back into a single line. Punctuation
// survives as separate words or inside a parenthesized word, so the token
// stream, the only thing the clause scanner inspects, is identical to the
// original line's.
func reconstructLine(previousWords []string, text []rune) []rune {
	words := make([]string, 0, len(previousWords)+1)
	for i := len(previousWords) - 1; i >= 0; i-- {
		words = append(words, previousWords[i])
	}
	words = append(words, string(text))
	return []rune(strings.Join(words, " "))
}

// completeFromContext generates the candidates for a parsed context: tables
// in table positions (fully qualified as schema.table, so candidates are
// self-describing), columns in column positions. Candidates are full words
// that replace the word at the cursor (see readline.Replacer), ranked
// fuzzily — prefix matches first. It returns nil when the context does not
// determine a candidate set, so callers can fall back to the heuristics.
func (c completer) completeFromContext(ctx Context) [][]rune {
	options, ok := c.contextOptions(ctx)
	if !ok || len(options) == 0 {
		return nil
	}
	return completeFuzzyFull(ctx.Qualifier+ctx.Object, options)
}

// DoRepl provides the fork's replace-style completion: the context path's
// candidates are full words replacing the word at the cursor. When the
// context is inconclusive it returns ok=false, and the readline layer falls
// back to Do (append-style heuristics).
func (c *completer) DoRepl(line []rune, pos int) ([][]rune, int, bool) {
	var i int
	for i = pos - 1; i > 0; i-- {
		if strings.ContainsRune(WORD_BREAKS, line[i]) {
			i++
			break
		}
	}
	if i == -1 {
		i = 0
	}
	previousWords := getPreviousWords(pos, line)
	text := line[i:pos]
	if res := c.completeWithContext(previousWords, text); res != nil {
		return res, len(text), true
	}
	// meta-command argument position (the command itself is already
	// typed): object and value candidates are full words replacing the
	// argument. Typing the command itself (text starts with backslash)
	// stays append-style, since backslash command candidates are suffixes.
	if !strings.HasPrefix(string(text), "\\") && len(previousWords) > 0 &&
		strings.HasPrefix(previousWords[len(previousWords)-1], "\\") {
		if res := c.complete(previousWords, text); res != nil {
			return res, len(text), true
		}
	}
	return nil, 0, false
}

// contextOptions returns the candidate options for the context, or ok=false
// when the context is inconclusive and the caller should fall back to the
// tail-matching heuristics.
func (c completer) contextOptions(ctx Context) ([]string, bool) {
	switch {
	case ctx.Qualifier != "" && ctx.Clause != "":
		return c.qualifiedOptions(ctx), true
	case ctx.Clause == "INTO" && ctx.OpenParens > 0:
		// INSERT INTO film (<cursor> — the column list of the target table
		return c.scopeColumns(ctx), len(ctx.Tables) > 0
	case ctx.Clause == "INTO" && ctx.IntoListDone:
		// INSERT INTO film (a, b) <cursor> — what may follow the column
		// group
		return []string{"VALUES", "SELECT", "TABLE", "OVERRIDING"}, true
	case ctx.Clause == "USING" && ctx.OpenParens > 0:
		// JOIN ... USING (<cursor> — join columns of the tables in scope
		return c.scopeColumns(ctx), len(ctx.Tables) > 0
	case tableClauses[ctx.Clause]:
		if ctx.Object == "" && ctx.TableListed {
			if ctx.Clause == "INTO" {
				// INSERT INTO film <cursor> — offer the target table's
				// full column list as one "(a, b, c)" candidate, in
				// metadata order; without column metadata decline to the
				// heuristics ("(", VALUES, ...)
				if list := c.insertColumnList(ctx); list != "" {
					return []string{list}, true
				}
			}
			return nil, false
		}
		// dotless words complete schema/user/owner names first, keeping
		// the candidate list small; after "schema." the qualified branch
		// lists that namespace's objects. Only when no namespace matches
		// the typed word fall back to fully qualified tables, so direct
		// table names ("FROM film") still complete.
		ns := c.getNamespaces(metadata.Filter{OnlyVisible: true})
		if len(completeFuzzyFull(ctx.Qualifier+ctx.Object, ns)) > 0 {
			return ns, true
		}
		return c.scopeTables(ctx, ctx.Clause == "INTO" || ctx.Clause == "UPDATE"), true
	case columnClauses[ctx.Clause]:
		options := c.scopeColumns(ctx)
		options = append(options, clauseKeywords[ctx.Clause]...)
		return options, true
	default:
		return nil, false
	}
}

// qualifiedOptions completes a dotted word. In a column position the
// qualifier names an alias or table (or schema.table), and the candidates
// are that table's columns; in a table position it names a schema (or
// catalog.schema) and the candidates are that namespace's tables. Candidates
// are fully qualified, so they replace the whole word including the
// qualifier.
func (c completer) qualifiedOptions(ctx Context) []string {
	parts := strings.Split(strings.TrimSuffix(ctx.Qualifier, "."), ".")
	qual := ctx.Qualifier
	appendColumns := func(ref TableRef) []string {
		var options []string
		for _, col := range c.tableColumns(ref) {
			options = append(options, qual+col)
		}
		return options
	}
	switch n := len(parts); {
	case n == 1 && tableClauses[ctx.Clause]:
		return c.namespaceTables("", parts[0], ctx.Clause == "INTO" || ctx.Clause == "UPDATE")
	case n == 1:
		// in a column position the qualifier is an alias or table name; if
		// it is neither, it may still be a schema
		if ref, ok := ctx.Aliases[parts[0]]; ok {
			return appendColumns(ref)
		}
		return c.namespaceTables("", parts[0], true)
	case n == 2 && tableClauses[ctx.Clause]:
		// remote.default.<cursor>
		return c.namespaceTables(parts[0], parts[1], ctx.Clause == "INTO" || ctx.Clause == "UPDATE")
	case n == 2:
		// public.film.<cursor>
		return appendColumns(TableRef{Schema: parts[0], Name: parts[1]})
	default:
		// catalog.schema.table.<cursor>
		return appendColumns(TableRef{Catalog: parts[n-3], Schema: parts[n-2], Name: parts[n-1]})
	}
}

// insertColumnList renders the full column list of the INSERT INTO target
// table as a single "(a, b, c)" candidate, in metadata (ordinal) order. It
// returns "" when the columns cannot be determined.
func (c completer) insertColumnList(ctx Context) string {
	if len(ctx.Tables) == 0 {
		return ""
	}
	ref := ctx.Tables[len(ctx.Tables)-1]
	r, ok := c.reader.(metadata.ColumnReader)
	if !ok {
		return ""
	}
	filter := metadata.Filter{
		Catalog:     ref.Catalog,
		Schema:      ref.Schema,
		Parent:      ref.Name,
		OnlyVisible: ref.Catalog == "" && ref.Schema == "",
		WithSystem:  ref.Catalog != "" || ref.Schema != "",
	}
	set, err := r.Columns(filter)
	if err != nil {
		return ""
	}
	defer set.Close()
	var cols []string
	for set.Next() {
		cols = append(cols, set.Get().Name)
	}
	if len(cols) == 0 {
		return ""
	}
	return "(" + strings.Join(cols, ", ") + ")"
}

// scopeColumns returns the columns of every table in scope, in metadata
// order, deduplicated by name.
func (c completer) scopeColumns(ctx Context) []string {
	var options []string
	seen := map[string]bool{}
	for _, ref := range ctx.Tables {
		for _, col := range c.tableColumns(ref) {
			if !seen[col] {
				seen[col] = true
				options = append(options, col)
			}
		}
	}
	return options
}

// tableColumns queries the reader for the columns of a table reference, in
// metadata (ordinal) order.
func (c completer) tableColumns(ref TableRef) []string {
	r, ok := c.reader.(metadata.ColumnReader)
	if !ok || ref.Name == "" {
		return nil
	}
	filter := metadata.Filter{
		Catalog:     ref.Catalog,
		Schema:      ref.Schema,
		Parent:      ref.Name,
		OnlyVisible: ref.Catalog == "" && ref.Schema == "",
		WithSystem:  ref.Catalog != "" || ref.Schema != "",
	}
	set, err := r.Columns(filter)
	if err != nil {
		return nil
	}
	defer set.Close()
	var cols []string
	for set.Next() {
		cols = append(cols, set.Get().Name)
	}
	return cols
}

// scopeTables is the fallback for a table position, used only when no
// namespace matches the typed word: fully qualified selectables — all of
// them for FROM and JOIN, updatable tables only for INSERT INTO and UPDATE.
// The query filter is stable (no typed text in it): per-keystroke matching
// happens client-side in fuzzy ranking, so the cached query is reused while
// typing.
func (c completer) scopeTables(ctx Context, tablesOnly bool) []string {
	filter := metadata.Filter{OnlyVisible: true}
	if tablesOnly {
		filter.Types = updatableTypes
	}
	var names []string
	if r, ok := c.reader.(metadata.TableReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Tables(filter) },
			func(res interface{}) string {
				t := res.(*metadata.TableSet).Get()
				// schema.table (catalog.schema.table) — full names tell
				// similarly-named objects apart
				return fullIdentifier(t.Catalog, t.Schema, t.Name)
			},
		)...)
	}
	if tablesOnly {
		return names
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Functions(filter) },
			func(res interface{}) string {
				f := res.(*metadata.FunctionSet).Get()
				return fullIdentifier(f.Catalog, f.Schema, f.Name)
			},
		)...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Sequences(filter) },
			func(res interface{}) string {
				s := res.(*metadata.SequenceSet).Get()
				return fullIdentifier(s.Catalog, s.Schema, s.Name)
			},
		)...)
	}
	return names
}

// namespaceTables completes the tables of a schema, or of a catalog and
// schema — plus, unless tablesOnly, its functions and sequences — returning
// fully qualified names. The filter is stable across keystrokes; the typed
// object text is matched client-side by fuzzy ranking.
func (c completer) namespaceTables(catalog, schema string, tablesOnly bool) []string {
	filter := metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true}
	names := make([]string, 0, 10)
	if r, ok := c.reader.(metadata.TableReader); ok {
		if tablesOnly {
			filter.Types = updatableTypes
		}
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Tables(filter) },
			func(res interface{}) string {
				t := res.(*metadata.TableSet).Get()
				return qualifiedIdentifier(filter, t.Catalog, t.Schema, t.Name)
			},
		)...)
	}
	if tablesOnly {
		return names
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Functions(filter) },
			func(res interface{}) string {
				f := res.(*metadata.FunctionSet).Get()
				return fullIdentifier(f.Catalog, f.Schema, f.Name)
			},
		)...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Sequences(filter) },
			func(res interface{}) string {
				s := res.(*metadata.SequenceSet).Get()
				return fullIdentifier(s.Catalog, s.Schema, s.Name)
			},
		)...)
	}
	return names
}
