package completer

import (
	"sort"
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
			c.reader = NewCachedReader(c.reader)
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
// in table positions, columns in column positions, fuzzy-ranked against the
// full word being completed (qualifier included). It returns nil when the
// context does not determine a candidate set, so callers can fall back to
// the heuristics.
func (c completer) completeFromContext(ctx Context) [][]rune {
	options, ok := c.contextOptions(ctx)
	if !ok || len(options) == 0 {
		return nil
	}
	sort.Strings(options)
	return completeFuzzy([]rune(ctx.Qualifier+ctx.Object), options...)
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
	case ctx.Clause == "USING" && ctx.OpenParens > 0:
		// JOIN ... USING (<cursor> — join columns of the tables in scope
		return c.scopeColumns(ctx), len(ctx.Tables) > 0
	case tableClauses[ctx.Clause]:
		if ctx.Object == "" && ctx.TableListed {
			// the table of the clause is already listed; what comes next
			// ("(", SET, VALUES, ...) is the heuristics' business
			return nil, false
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

// scopeColumns returns the columns of every table in scope.
func (c completer) scopeColumns(ctx Context) []string {
	var options []string
	for _, ref := range ctx.Tables {
		options = append(options, c.tableColumns(ref)...)
	}
	return options
}

// tableColumns queries the reader for the columns of a table reference.
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
	return c.getNames(
		func() (iterator, error) { return r.Columns(filter) },
		func(res interface{}) string {
			return res.(*metadata.ColumnSet).Get().Name
		},
	)
}

// scopeTables completes the first table of a statement: namespaces to qualify
// with, plus the tables themselves — all selectables for FROM and JOIN,
// updatable tables only for INSERT INTO and UPDATE, mirroring
// completeWithSelectables and completeWithUpdatables. The query filter is
// stable (no typed text in it): per-keystroke matching happens client-side
// in fuzzy ranking, so the cached query is reused while typing.
func (c completer) scopeTables(ctx Context, tablesOnly bool) []string {
	filter := metadata.Filter{OnlyVisible: true}
	if tablesOnly {
		filter.Types = updatableTypes
	}
	names := c.getNamespaces(filter)
	if r, ok := c.reader.(metadata.TableReader); ok {
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
				return qualifiedIdentifier(filter, f.Catalog, f.Schema, f.Name)
			},
		)...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Sequences(filter) },
			func(res interface{}) string {
				s := res.(*metadata.SequenceSet).Get()
				return qualifiedIdentifier(filter, s.Catalog, s.Schema, s.Name)
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
				return qualifiedIdentifier(filter, f.Catalog, f.Schema, f.Name)
			},
		)...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		names = append(names, c.getNames(
			func() (iterator, error) { return r.Sequences(filter) },
			func(res interface{}) string {
				s := res.(*metadata.SequenceSet).Get()
				return qualifiedIdentifier(filter, s.Catalog, s.Schema, s.Name)
			},
		)...)
	}
	return names
}
