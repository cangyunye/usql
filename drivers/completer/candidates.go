package completer

import (
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
	"github.com/xo/usql/text"
)

// updatableTypes are the table types offered in INSERT INTO and UPDATE
// positions.
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

// verbFollows are the keywords that must follow a statement verb when no
// clause has been recognized yet: "INSERT <cursor>" takes INTO, "DELETE
// <cursor>" takes FROM. The context engine offers exactly that keyword and
// nothing else — no query.
var verbFollows = map[string]string{
	"INSERT": "INTO",
	"DELETE": "FROM",
}

// builtinFunc is one built-in function completion: args is the signature
// shown as dim detail; bare functions take no parentheses.
type builtinFunc struct {
	name string
	args string
	bare bool
}

// builtinFunctions is the dialect-neutral core offered in expression
// positions. Stored routines come from the catalog cache; these cover the
// everyday calls on engines whose catalogs enumerate only stored routines
// (MySQL-family information_schema.routines), and on engines without a
// routine catalog at all — at zero query cost.
var builtinFunctions = []builtinFunc{
	{name: "COUNT", args: "expr"},
	{name: "SUM", args: "expr"},
	{name: "AVG", args: "expr"},
	{name: "MIN", args: "expr"},
	{name: "MAX", args: "expr"},
	{name: "COALESCE", args: "expr, .."},
	{name: "NULLIF", args: "a, b"},
	{name: "CAST", args: "expr AS type"},
	{name: "EXTRACT", args: "field FROM source"},
	{name: "CONCAT", args: "str, .."},
	{name: "SUBSTRING", args: "str, pos, len"},
	{name: "UPPER", args: "str"},
	{name: "LOWER", args: "str"},
	{name: "LENGTH", args: "str"},
	{name: "TRIM", args: "str"},
	{name: "REPLACE", args: "str, from, to"},
	{name: "ABS", args: "x"},
	{name: "ROUND", args: "x, d"},
	{name: "FLOOR", args: "x"},
	{name: "CEIL", args: "x"},
	{name: "MOD", args: "a, b"},
	{name: "POWER", args: "a, b"},
	{name: "CURRENT_DATE", bare: true},
	{name: "CURRENT_TIME", bare: true},
	{name: "CURRENT_TIMESTAMP", bare: true},
	{name: "CURRENT_USER", bare: true},
	{name: "SESSION_USER", bare: true},
}

// functionCand renders a function completion: the opening paren is part of
// the inserted text, and the signature — the argument types closing it,
// plus the result type when known — rides as dim detail. A candidate with
// an empty sig still inserts "name(".
func functionCand(name, sig string) rline.Cand {
	return rline.Cand{Text: name + "(", Kind: "function", Detail: sig}
}

// callSignature renders a cached function's dim detail from its argument
// and result types ("x numeric, y text) → integer"); an argument-less
// function closes its own paren ("now(" + ")").
func callSignature(args, result string) string {
	sig := ")"
	if args != "" {
		sig = args + ")"
	}
	if result != "" {
		sig += " → " + result
	}
	return sig
}

// functionCands builds the function tier for expression positions: the
// current schema's catalog functions (L2; absent until their load lands)
// followed by the static built-ins, deduplicated by upper-cased name so a
// catalog function shadows its built-in twin. Candidates insert "name("
// and carry the signature as dim detail.
func (c completer) functionCands() []rline.Cand {
	seen := make(map[string]struct{}, 32)
	out := make([]rline.Cand, 0, 32)
	if cur, ok := c.currentSchema(); ok && cur != "" {
		if objs, loaded := c.schemaObjects("", cur); loaded {
			for _, o := range objs {
				if o.kind != "function" {
					continue
				}
				name := o.name
				if i := strings.LastIndexByte(name, '.'); i >= 0 {
					name = name[i+1:]
				}
				if _, dup := seen[strings.ToUpper(name)]; dup {
					continue
				}
				seen[strings.ToUpper(name)] = struct{}{}
				out = append(out, functionCand(name, o.detail))
			}
		}
	}
	for _, f := range builtinFunctions {
		if _, dup := seen[f.name]; dup {
			continue
		}
		seen[f.name] = struct{}{}
		if f.bare {
			out = append(out, rline.Cand{Text: f.name, Kind: "function"})
			continue
		}
		out = append(out, functionCand(f.name, f.args+")"))
	}
	return out
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
// calls it after statements that change the session scope, and \refresh
// calls it directly.
func (c *completer) Invalidate() {
	if c.cache != nil {
		c.cache.Invalidate()
	}
	if c.caps != nil {
		c.caps.forget()
	}
}

// catalogCaps remembers, per catalog kind ("tables", "functions",
// "sequences", ...), that the connected database does not serve it. Engines
// differ — MySQL has no sequences, others lack functions — so one
// ErrNotSupported retires the kind for the whole session: its queries are
// never re-issued (no per-keystroke error storms) and not logged as errors.
type catalogCaps struct {
	mu          sync.Mutex
	unsupported map[string]bool
	logged      map[string]bool
}

func newCatalogCaps() *catalogCaps {
	return &catalogCaps{
		unsupported: map[string]bool{},
		logged:      map[string]bool{},
	}
}

// skip reports whether the kind is known to be unsupported.
func (cc *catalogCaps) skip(kind string) bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.unsupported[kind]
}

// failed records a load error for the kind: ErrNotSupported retires it for
// the session; anything else keeps the kind enabled and is logged once —
// loaders retry on later requests, so an unthrottled log would repeat per
// keystroke.
func (cc *catalogCaps) failed(kind string, err error, log logger) {
	cc.mu.Lock()
	if errors.Is(err, text.ErrNotSupported) {
		cc.unsupported[kind] = true
		cc.mu.Unlock()
		return
	}
	first := !cc.logged[kind]
	cc.logged[kind] = true
	cc.mu.Unlock()
	if first && log != nil {
		log.Println("completion:", kind, "load failed:", err)
	}
}

// forget drops the retirement marks, so a scope change or manual refresh
// re-probes every kind.
func (cc *catalogCaps) forget() {
	cc.mu.Lock()
	cc.unsupported = map[string]bool{}
	cc.logged = map[string]bool{}
	cc.mu.Unlock()
}

// WithContextCompletion returns an Option that installs the lazy catalog
// cache behind the context engine. The cache has three levels — the
// schemas visible to the login (L1), a named schema's objects (L2) and a
// table's columns (L3) — all loaded on demand and asynchronously: connecting
// issues no metadata queries, and typing never blocks on the database. When
// a load lands, the cache's kick re-renders the pending completion.
func WithContextCompletion() Option {
	return func(c *completer) {
		if c.reader != nil {
			c.cache = newCatalogCache()
			c.caps = newCatalogCaps()
		}
	}
}

// SetKick wires the cache's re-render hook (used by the live wrapper).
func (c *completer) SetKick(f func()) {
	if c.cache != nil {
		c.cache.SetKick(f)
	}
}

// completeWithContext is the context engine's entry point: it rebuilds the
// line up to the cursor from the previous words, parses the clause context,
// and generates candidates from it. It returns nil when the context is
// inconclusive (the caller falls through to the driver hook and the keyword
// fallback) or when the cursor sits in a string or comment — nothing is
// completed there.
func (c completer) completeWithContext(previousWords []string, text []rune) []rline.Cand {
	line := reconstructLine(previousWords, text)
	if inStringOrComment(line) {
		return nil
	}
	return c.completeFromContext(parseContext(line, len(line)))
}

// inStringOrComment reports whether the end of the line — the cursor — sits
// inside an unterminated string literal, quoted identifier or comment:
// completion offers nothing there.
func inStringOrComment(line []rune) bool {
	var inSingle, inDouble, inBlock, inLine bool
	for i := 0; i < len(line); i++ {
		r := line[i]
		switch {
		case inLine:
			return true // a line comment runs to the cursor
		case inBlock:
			if r == '*' && i+1 < len(line) && line[i+1] == '/' {
				inBlock = false
				i++
			}
		case inSingle:
			if r == '\'' {
				if i+1 < len(line) && line[i+1] == '\'' {
					i++ // escaped ''
				} else {
					inSingle = false
				}
			}
		case inDouble:
			if r == '"' {
				inDouble = false
			}
		case r == '-' && i+1 < len(line) && line[i+1] == '-':
			inLine = true
			i++
		case r == '/' && i+1 < len(line) && line[i+1] == '*':
			inBlock = true
			i++
		case r == '\'':
			inSingle = true
		case r == '"':
			inDouble = true
		}
	}
	return inSingle || inDouble || inBlock
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

// completeFromContext generates the candidates for a parsed context: the
// schema tier in object positions, a namespace's objects after "schema.",
// columns in column positions. Candidates are full words that replace the
// word at the cursor (see readline.Replacer). It returns nil when the
// context does not determine a candidate set or when the context's data is
// still loading — the cache's kick re-renders when it lands.
func (c completer) completeFromContext(ctx Context) []rline.Cand {
	options, ok, ordered := c.contextOptions(ctx)
	if !ok || len(options) == 0 {
		return nil
	}
	if ordered {
		// already in the order the user needs (e.g. VALUES field hints);
		// ranking would destroy it
		return options
	}
	// anchored matching: candidates match from the start of the word or of
	// their last segment, not as fuzzy subsequences
	return completePrefixFull(ctx.Qualifier+ctx.Object, options)
}

// DoRepl provides replace-style completion: the context engine's candidates
// are full words replacing the word at the cursor, and a meta-command
// argument position completes through the policy table. When neither
// applies it returns ok=false, and the readline layer falls back to Do
// (append-style meta and keyword candidates).
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
	// meta-command argument position (the command itself is already typed):
	// object and value candidates are full words replacing the argument
	if !strings.HasPrefix(string(text), "\\") && len(previousWords) > 0 &&
		strings.HasPrefix(previousWords[len(previousWords)-1], "\\") {
		if res := c.completeMetaArg(previousWords[len(previousWords)-1], len(previousWords)-1, previousWords, text); res != nil {
			return res, len(text), true
		}
	}
	if res := c.completeWithContext(previousWords, text); res != nil {
		return res, len(text), true
	}
	// a dotted word never falls back to keywords: it names an object whose
	// metadata is still loading (or unknown) — a keyword flash would be
	// wrong every time
	if strings.Contains(string(text), ".") {
		return nil, 0, false
	}
	return nil, 0, false
}

// contextOptions returns the candidate options for the context, or ok=false
// when the context is inconclusive (the caller falls through to the driver
// hook and the keyword fallback). The options depend only on the clause
// context — never on the typed word — so the client filters them per
// keystroke and the cache is reused while typing. ordered results (the
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
		// order (name:type, so long column lists stay trackable while
		// filling values); once a value is being typed, the matching
		// function calls follow after the hints. The hints are ordered and
		// exempt from prefix filtering, so the function tier is filtered
		// here.
		options := c.valuesFieldHints(ctx)
		if ctx.CallName == "" && ctx.Object != "" {
			options = append(options, completePrefixFull(ctx.Qualifier+ctx.Object, c.functionCands())...)
		}
		return options, true, true
	case ctx.Clause == "INTO" && ctx.IntoListDone:
		// INSERT INTO film (a, b) <cursor> — what may follow the column group
		return rline.Cands("VALUES", "SELECT", "TABLE", "OVERRIDING"), true, false
	case ctx.Clause == "USING" && ctx.OpenParens > 0:
		// JOIN ... USING (<cursor> — join columns of the tables in scope
		return candsText(c.scopeColumns(ctx), "column"), len(ctx.Tables) > 0, false
	case tableClauses[ctx.Clause]:
		if ctx.Object == "" && ctx.TableListed {
			if ctx.Clause == "INTO" {
				// INSERT INTO film <cursor> — offer the target table's full
				// column list as one "(a, b, c)" candidate, in metadata
				// order; without column metadata decline ("(", VALUES, ...)
				if list := c.insertColumnList(ctx); list != "" {
					return []rline.Cand{{Text: list}}, true, false
				}
			}
			return nil, false, false
		}
		// object position: the schema tier (L1) first — pick a namespace,
		// then "schema." completes its objects (L2). The current schema's
		// own objects ride along once their (lazy) L2 load has landed, so
		// bare object names keep completing.
		cands := c.namespaceCands()
		tablesOnly := ctx.Clause == "INTO" || ctx.Clause == "UPDATE"
		if cur, ok := c.currentSchema(); ok && cur != "" {
			if objs, loaded := c.schemaObjects("", cur); loaded {
				cands = append(cands, candsObjs(selectableObjs(objs, tablesOnly))...)
			}
		}
		if len(cands) == 0 {
			return nil, false, false // still loading: decline, kick re-renders
		}
		return cands, true, false
	case columnClauses[ctx.Clause]:
		options := candsText(c.scopeColumns(ctx), "column")
		// the function tier joins the columns once a word is being typed —
		// an empty word keeps the menu to the columns and clause keywords —
		// and stands down inside a call's argument list (ctx.CallName),
		// where nested calls are typed, not picked
		if ctx.CallName == "" && ctx.Object != "" {
			options = append(options, c.functionCands()...)
		}
		options = append(options, completeFromList(nil, clauseKeywords[ctx.Clause]...)...)
		return options, true, false
	case ctx.Clause == "" && verbFollows[ctx.First] != "":
		// a statement verb that must be followed by one keyword
		return rline.Cands(verbFollows[ctx.First]), true, false
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

// candsObjs converts cached objects into candidates.
func candsObjs(objs []obj) []rline.Cand {
	out := make([]rline.Cand, 0, len(objs))
	for _, o := range objs {
		out = append(out, rline.Cand{Text: o.name, Kind: o.kind, Detail: o.detail})
	}
	return out
}

// selectableObjs filters cached schema objects: INSERT INTO and UPDATE
// positions cannot take functions or sequences, so those are dropped there.
func selectableObjs(objs []obj, relationsOnly bool) []obj {
	if !relationsOnly {
		return objs
	}
	out := make([]obj, 0, len(objs))
	for _, o := range objs {
		switch o.kind {
		case "function", "sequence":
		default:
			out = append(out, o)
		}
	}
	return out
}

// valuesFieldHints hints the fields of an INSERT ... VALUES group in
// written order, starting at the value currently being filled. Each hint
// shows the column name with its data type as dim detail — "city_id" plus
// ":number(10)". The column order comes from the written "(a, b, c)" list,
// resolved against the target table's metadata for the types, or from the
// table's metadata directly when no explicit list was given.
func (c completer) valuesFieldHints(ctx Context) []rline.Cand {
	var cols []obj
	if len(ctx.IntoColumns) > 0 {
		cols = make([]obj, 0, len(ctx.IntoColumns))
		var types map[string]string
		if len(ctx.Tables) > 0 {
			types = make(map[string]string)
			for _, o := range c.tableColumnObjs(ctx.Tables[len(ctx.Tables)-1]) {
				types[strings.ToUpper(o.name)] = o.detail
			}
		}
		for _, name := range ctx.IntoColumns {
			cols = append(cols, obj{name: name, detail: types[strings.ToUpper(name)]})
		}
	} else if len(ctx.Tables) > 0 {
		cols = c.tableColumnObjs(ctx.Tables[len(ctx.Tables)-1])
	}
	if ctx.ValuesCount >= len(cols) {
		// more values than columns: nothing left to hint
		return nil
	}
	cols = cols[ctx.ValuesCount:]
	out := make([]rline.Cand, 0, len(cols))
	for _, o := range cols {
		d := ""
		if o.detail != "" {
			d = ":" + o.detail
		}
		out = append(out, rline.Cand{Text: o.name, Detail: d, Kind: "column"})
	}
	return out
}

// qualifiedOptions completes a dotted word. In a table position the
// qualifier names a schema (or catalog.schema) and the candidates are that
// namespace's objects (L2); in a column position it names an alias, table
// or schema.table, and the candidates are that table's columns (L3).
// Candidates are fully qualified, so they replace the whole word including
// the qualifier.
func (c completer) qualifiedOptions(ctx Context) []rline.Cand {
	parts := strings.Split(strings.TrimSuffix(ctx.Qualifier, "."), ".")
	qual := ctx.Qualifier
	tablesOnly := ctx.Clause == "INTO" || ctx.Clause == "UPDATE"
	appendColumns := func(ref TableRef) []rline.Cand {
		var options []rline.Cand
		for _, col := range c.tableColumns(ref) {
			options = append(options, rline.Cand{Text: qual + col, Kind: "column"})
		}
		return options
	}
	schemaObjects := func(catalog, schema string, relationsOnly bool) []rline.Cand {
		objs, ok := c.schemaObjects(catalog, schema)
		if !ok {
			return nil // still loading: decline, kick re-renders
		}
		return candsObjs(selectableObjs(objs, relationsOnly))
	}
	switch n := len(parts); {
	case n == 1 && tableClauses[ctx.Clause]:
		// FROM sc.<cursor> — the schema's objects
		return schemaObjects("", parts[0], tablesOnly)
	case n == 1:
		// in a column position the qualifier is an alias or table name; if
		// it is neither, it may still be a schema (its relations)
		if ref, ok := ctx.Aliases[parts[0]]; ok {
			return appendColumns(ref)
		}
		return schemaObjects("", parts[0], true)
	case n == 2 && tableClauses[ctx.Clause]:
		// remote.default.<cursor>
		return schemaObjects(parts[0], parts[1], tablesOnly)
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
	cols := c.tableColumns(ctx.Tables[len(ctx.Tables)-1])
	if len(cols) == 0 {
		return ""
	}
	return "(" + strings.Join(cols, ", ") + ")"
}

// scopeColumns returns the columns of every table in scope, in metadata
// order, deduplicated by name. Tables whose L3 load has not landed yet
// contribute nothing this round.
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

// tableColumns returns the columns of a table reference, bare names, in
// metadata (ordinal) order.
func (c completer) tableColumns(ref TableRef) []string {
	objs := c.tableColumnObjs(ref)
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, o.name)
	}
	return names
}

// tableColumnObjs returns the columns of a table reference as cached
// objects — name plus data-type detail — in metadata (ordinal) order. A
// cache miss starts the asynchronous load and returns nothing this round.
// Without a cache installed the reader is queried directly.
func (c completer) tableColumnObjs(ref TableRef) []obj {
	if ref.Name == "" {
		return nil
	}
	if c.cache == nil {
		return c.queryColumnObjs(ref)
	}
	objs, _ := c.columns(ref)
	return objs
}

// --- cached catalog access (the three levels) ---

// namespaceCands returns the L1 schema list as candidates. A nil result
// means the load has not landed yet.
func (c completer) namespaceCands() []rline.Cand {
	objs, ok := c.schemas()
	if !ok {
		return nil
	}
	return candsObjs(objs)
}

func (c completer) schemas() ([]obj, bool) {
	if c.cache == nil {
		return nil, false
	}
	return c.cache.get(bucketSchema, "all", c.loadSchemas)
}

// currentSchema returns the session's default schema, resolved lazily with
// its own L1 entry. MySQL names it via DATABASE(), PostgreSQL via
// search_path, Oracle via CURRENT_SCHEMA — the readers express each as the
// OnlyVisible restriction, so one query serves all.
func (c completer) currentSchema() (string, bool) {
	if c.cache == nil {
		return "", false
	}
	objs, ok := c.cache.get(bucketSchema, "current", c.loadCurrentSchema)
	if !ok || len(objs) == 0 {
		return "", false
	}
	return objs[0].name, true
}

func (c completer) schemaObjects(catalog, schema string) ([]obj, bool) {
	if c.cache == nil || schema == "" {
		return nil, false
	}
	key := catalog + "\x00" + schema
	return c.cache.get(bucketObject, key, func() ([]obj, error) {
		return c.loadSchemaObjects(catalog, schema)
	})
}

func (c completer) columns(ref TableRef) ([]obj, bool) {
	if c.cache == nil {
		return nil, false
	}
	key := ref.Catalog + "\x00" + ref.Schema + "\x00" + ref.Name
	return c.cache.get(bucketColumn, key, func() ([]obj, error) {
		return c.loadColumns(ref)
	})
}

// loadSchemas loads L1: every schema the login can reach, minus the system
// schemas (the readers' WithSystem:false restriction).
func (c completer) loadSchemas() ([]obj, error) {
	r, ok := c.reader.(metadata.SchemaReader)
	if !ok {
		return []obj{}, nil
	}
	if c.caps.skip("schemas") {
		return []obj{}, nil
	}
	set, err := r.Schemas(metadata.Filter{WithSystem: false})
	if err != nil {
		c.caps.failed("schemas", err, c.logger)
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		s := set.Get()
		out = append(out, obj{name: s.Schema, kind: c.schemaKind})
	}
	return out, nil
}

// loadCurrentSchema loads the session's default schema (L1, dedicated key).
func (c completer) loadCurrentSchema() ([]obj, error) {
	r, ok := c.reader.(metadata.SchemaReader)
	if !ok {
		return []obj{}, nil
	}
	set, err := r.Schemas(metadata.Filter{OnlyVisible: true})
	if err != nil {
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() && len(out) == 0 {
		out = append(out, obj{name: set.Get().Schema, kind: c.schemaKind})
	}
	return out, nil
}

// loadSchemaObjects loads L2: a namespace's relations, functions and
// sequences, with fully qualified names. One bucket per schema — typing
// "schema." is what triggers it. Each kind loads and fails independently:
// an engine without a catalog (MySQL has no information_schema.sequences)
// skips that kind instead of failing the whole bucket, so the rest stays
// cached.
func (c completer) loadSchemaObjects(catalog, schema string) ([]obj, error) {
	var out []obj
	if r, ok := c.reader.(metadata.TableReader); ok && !c.caps.skip("tables") {
		filter := metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true, Types: selectableTypes}
		if set, err := r.Tables(filter); err != nil {
			c.caps.failed("tables", err, c.logger)
		} else {
			for set.Next() {
				t := set.Get()
				out = append(out, obj{name: fullIdentifier(t.Catalog, t.Schema, t.Name), kind: typeKind(t.Type)})
			}
			set.Close()
		}
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok && !c.caps.skip("functions") {
		filter := metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true}
		if set, err := r.Functions(filter); err != nil {
			c.caps.failed("functions", err, c.logger)
		} else {
			for set.Next() {
				f := set.Get()
				out = append(out, obj{
					name:   fullIdentifier(f.Catalog, f.Schema, f.Name),
					kind:   "function",
					detail: callSignature(f.ArgTypes, f.ResultType),
				})
			}
			set.Close()
		}
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok && !c.caps.skip("sequences") {
		filter := metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true}
		if set, err := r.Sequences(filter); err != nil {
			c.caps.failed("sequences", err, c.logger)
		} else {
			for set.Next() {
				s := set.Get()
				out = append(out, obj{name: fullIdentifier(s.Catalog, s.Schema, s.Name), kind: "sequence"})
			}
			set.Close()
		}
	}
	return out, nil
}

// loadColumns loads L3: a table's columns, bare names.
func (c completer) loadColumns(ref TableRef) ([]obj, error) {
	r, ok := c.reader.(metadata.ColumnReader)
	if !ok || c.caps.skip("columns") {
		return []obj{}, nil // unsupported: cache the emptiness
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
		c.caps.failed("columns", err, c.logger)
		if errors.Is(err, text.ErrNotSupported) {
			return []obj{}, nil
		}
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		col := set.Get()
		out = append(out, obj{name: col.Name, kind: "column", detail: col.DataType})
	}
	return out, nil
}

// queryColumnObjs queries the reader directly — the fallback when no cache
// is installed.
func (c completer) queryColumnObjs(ref TableRef) []obj {
	r, ok := c.reader.(metadata.ColumnReader)
	if !ok {
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
	var out []obj
	for set.Next() {
		col := set.Get()
		out = append(out, obj{name: col.Name, kind: "column", detail: col.DataType})
	}
	return out
}
