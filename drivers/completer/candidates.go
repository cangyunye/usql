package completer

import (
	"regexp"
	"strings"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// updatableTypes are the table types offered in INSERT INTO and UPDATE
// positions, mirroring completeWithUpdatables.
var updatableTypes = []string{"TABLE", "BASE TABLE", "LOCAL TEMPORARY", "GLOBAL TEMPORARY", "VIEW"}

// selectableTypes are the relation types offered in FROM/JOIN positions:
// updatable types plus materialized views. Indexes are never selectable.
var selectableTypes = append(updatableTypes, "MATERIALIZED VIEW")

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

// Invalidate drops all cached completion metadata and schedules a snapshot
// reload. The installed completer (as wrapped by NewLive) satisfies
// interface{ Invalidate() }; the handler calls it after statements that
// change the session scope.
func (c *completer) Invalidate() {
	if c.cache != nil {
		c.cache.clear()
	}
	if c.snap != nil {
		c.snap.Invalidate()
	}
}

// snapState reports the catalog snapshot's version and readiness, so the
// live wrapper can stamp its caches and drop pre-snapshot results when the
// snapshot supersedes them.
func (c completer) snapState() (int64, bool) {
	if c.snap == nil {
		return 0, false
	}
	return c.snap.state()
}

// WithContextCompletion returns an Option that tries context-aware candidate
// generation — clause scanning, alias resolution and fuzzy ranking — before
// the tail-matching heuristics run. Whenever the statement context does not
// determine a candidate set, completion falls through to the heuristics, so
// existing completions (backslash commands, variables, INSERT INTO ...
// VALUES, ...) are unaffected.
//
// The context path is also what makes typing-time completion cheap: its
// candidate options depend only on the clause context (never on the typed
// word), so the live wrapper memoizes the option set once per statement
// context and re-filters it client-side as the word grows.
//
// Any beforeComplete hook set by an earlier option (e.g. a driver's own
// completion, as in drivers/metadata/mysql) is preserved and tried after
// this one, so drivers' NewCompleter implementations compose by prepending
// their options before the caller's.
func WithContextCompletion() Option {
	return func(c *completer) {
		// cache metadata queries, so typing-time completion stays responsive
		// even when the catalog is slow (e.g. OceanBase), and snapshot the
		// visible objects once per connection: keystroke-time queries for
		// tables/functions/sequences/schemas filter the snapshot in memory
		if c.reader != nil {
			c.cache = NewCachedReader(c.reader).(*cachedReader)
			c.snap = NewSnapshotReader(c.cache)
			c.reader = c.snap
		}
		prev := c.beforeComplete
		c.beforeComplete = func(previousWords []string, text []rune) []rline.Cand {
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
func (c completer) completeWithContext(previousWords []string, text []rune) []rline.Cand {
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
func (c completer) completeFromContext(ctx Context) []rline.Cand {
	options, ok, ordered := c.contextOptions(ctx)
	if !ok || len(options) == 0 {
		return nil
	}
	if ordered {
		// already in the order the user needs (e.g. VALUES field hints);
		// fuzzy ranking would destroy it
		return options
	}
	// anchored (start-anchored) matching: candidates match from the start of
	// the word or of their last segment, not as fuzzy subsequences
	return completePrefixFull(ctx.Qualifier+ctx.Object, options)
}

// DoRepl provides the fork's replace-style completion: the context path's
// candidates are full words replacing the word at the cursor. When the
// context is inconclusive it returns ok=false, and the readline layer falls
// back to Do (append-style heuristics).
func (c *completer) DoRepl(line []rune, pos int) ([]rline.Cand, int, bool) {
	if pos > len(line) {
		pos = len(line)
	}
	// a terminator ends the statement: complete nothing while no new word is
	// typed, and complete the new statement as if typed on its own line
	if k := rline.LastStatementStart(line, pos); k > 0 {
		if rline.AtStatementEnd(line, pos) {
			return nil, 0, false
		}
		line, pos = line[k:], pos-k
	}
	i := wordStart(line, pos)
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
// tail-matching heuristics. The options depend only on the clause context —
// never on the typed word — so callers can filter them client-side and the
// live wrapper can memoize them per statement context. ordered results (the
// VALUES field hints) are position-sensitive and exempt from that.
func (c completer) contextOptions(ctx Context) ([]rline.Cand, bool, bool) {
	switch {
	case ctx.Qualifier != "" && ctx.Clause != "":
		return c.qualifiedOptions(ctx), true, false
	case ctx.Clause == "INTO" && ctx.OpenParens > 0:
		// INSERT INTO film (<cursor> — the column list of the target table
		return candsText(c.scopeColumns(ctx), "column"), len(ctx.Tables) > 0, false
	case ctx.Clause == "VALUES" && ctx.ValuesParens:
		// INSERT INTO ... VALUES (<cursor> — hint the fields in written
		// order, so long column lists stay trackable while filling values
		return candsText(c.valuesFieldHints(ctx), "column"), true, true
	case ctx.Clause == "INTO" && ctx.IntoListDone:
		// INSERT INTO film (a, b) <cursor> — what may follow the column
		// group
		return rline.Cands("VALUES", "SELECT", "TABLE", "OVERRIDING"), true, false
	case ctx.Clause == "USING" && ctx.OpenParens > 0:
		// JOIN ... USING (<cursor> — join columns of the tables in scope
		return candsText(c.scopeColumns(ctx), "column"), len(ctx.Tables) > 0, false
	case tableClauses[ctx.Clause]:
		if ctx.Object == "" && ctx.TableListed {
			if ctx.Clause == "INTO" {
				// INSERT INTO film <cursor> — offer the target table's
				// full column list as one "(a, b, c)" candidate, in
				// metadata order; without column metadata decline to the
				// heuristics ("(", VALUES, ...)
				if list := c.insertColumnList(ctx); list != "" {
					return []rline.Cand{{Text: list}}, true, false
				}
			}
			return nil, false, false
		}
		// table position: namespace names (schema/user/database) first, then
		// every fully qualified selectable — one word-independent option set
		// the client filters as the word grows. Catalogs (database names in
		// the PostgreSQL sense) are deliberately not offered: after FROM/
		// INTO one picks schema-qualified objects, not databases.
		ns := c.getNamespaces(metadata.Filter{OnlyVisible: true})
		return append(ns, c.scopeTables(ctx, ctx.Clause == "INTO" || ctx.Clause == "UPDATE")...), true, false
	case columnClauses[ctx.Clause]:
		options := candsText(c.scopeColumns(ctx), "column")
		options = append(options, CompleteFromList(nil, clauseKeywords[ctx.Clause]...)...)
		return options, true, false
	default:
		return nil, false, false
	}
}

// candsText wraps plain words as candidates of one kind.
func candsText(words []string, kind string) []rline.Cand {
	out := make([]rline.Cand, 0, len(words))
	for _, w := range words {
		out = append(out, rline.Cand{Text: w, Kind: kind})
	}
	return out
}

// valuesFieldHints hints the fields of an INSERT ... VALUES group in
// written order, starting at the value currently being filled. The column
// order comes from the written "(a, b, c)" list, or from the table's
// metadata when no explicit list was given.
func (c completer) valuesFieldHints(ctx Context) []string {
	cols := ctx.IntoColumns
	if len(cols) == 0 && len(ctx.Tables) > 0 {
		cols = c.tableColumns(ctx.Tables[len(ctx.Tables)-1])
	}
	if ctx.ValuesCount >= len(cols) {
		// more values than columns: nothing left to hint
		return nil
	}
	return cols[ctx.ValuesCount:]
}

// qualifiedOptions completes a dotted word. In a column position the
// qualifier names an alias or table (or schema.table), and the candidates
// are that table's columns; in a table position it names a schema (or
// catalog.schema) and the candidates are that namespace's tables. Candidates
// are fully qualified, so they replace the whole word including the
// qualifier.
func (c completer) qualifiedOptions(ctx Context) []rline.Cand {
	parts := strings.Split(strings.TrimSuffix(ctx.Qualifier, "."), ".")
	qual := ctx.Qualifier
	appendColumns := func(ref TableRef) []rline.Cand {
		var options []rline.Cand
		for _, col := range c.tableColumns(ref) {
			options = append(options, rline.Cand{Text: qual + col, Kind: "column"})
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

// scopeTables returns fully qualified selectables — all of them for FROM and
// JOIN, updatable tables only for INSERT INTO and UPDATE. The query filter
// is stable (no typed text in it): per-keystroke matching happens
// client-side in fuzzy ranking, so the cached query is reused while typing.
func (c completer) scopeTables(ctx Context, tablesOnly bool) []rline.Cand {
	filter := metadata.Filter{OnlyVisible: true}
	if tablesOnly {
		filter.Types = updatableTypes
	} else {
		filter.Types = selectableTypes
	}
	var names []rline.Cand
	if r, ok := c.reader.(metadata.TableReader); ok {
		names = append(names, c.getCands(
			func() (iterator, error) { return r.Tables(filter) },
			func(res interface{}) (string, string) {
				t := res.(*metadata.TableSet).Get()
				// schema.table (catalog.schema.table) — full names tell
				// similarly-named objects apart
				return fullIdentifier(t.Catalog, t.Schema, t.Name), typeKind(t.Type)
			},
		)...)
	}
	if tablesOnly {
		return names
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok {
		names = append(names, c.getCands(
			func() (iterator, error) { return r.Functions(filter) },
			func(res interface{}) (string, string) {
				f := res.(*metadata.FunctionSet).Get()
				return fullIdentifier(f.Catalog, f.Schema, f.Name), "function"
			},
		)...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		names = append(names, c.getCands(
			func() (iterator, error) { return r.Sequences(filter) },
			func(res interface{}) (string, string) {
				s := res.(*metadata.SequenceSet).Get()
				return fullIdentifier(s.Catalog, s.Schema, s.Name), "sequence"
			},
		)...)
	}
	return names
}

// namespaceTables completes the tables of a schema, or of a catalog and
// schema — plus, unless tablesOnly, its functions and sequences — returning
// fully qualified names with kinds. The filter is stable across keystrokes;
// the typed object text is matched client-side by fuzzy ranking.
func (c completer) namespaceTables(catalog, schema string, tablesOnly bool) []rline.Cand {
	filter := metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true}
	if tablesOnly {
		filter.Types = updatableTypes
	} else {
		filter.Types = selectableTypes
	}
	var names []rline.Cand
	if r, ok := c.reader.(metadata.TableReader); ok {
		names = append(names, c.getCands(
			func() (iterator, error) { return r.Tables(filter) },
			func(res interface{}) (string, string) {
				t := res.(*metadata.TableSet).Get()
				return qualifiedIdentifier(filter, t.Catalog, t.Schema, t.Name), typeKind(t.Type)
			},
		)...)
	}
	if tablesOnly {
		return names
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok {
		names = append(names, c.getCands(
			func() (iterator, error) { return r.Functions(filter) },
			func(res interface{}) (string, string) {
				f := res.(*metadata.FunctionSet).Get()
				return fullIdentifier(f.Catalog, f.Schema, f.Name), "function"
			},
		)...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		names = append(names, c.getCands(
			func() (iterator, error) { return r.Sequences(filter) },
			func(res interface{}) (string, string) {
				s := res.(*metadata.SequenceSet).Get()
				return fullIdentifier(s.Catalog, s.Schema, s.Name), "sequence"
			},
		)...)
	}
	return names
}
