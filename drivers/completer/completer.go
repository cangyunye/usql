// completer package provides a generic SQL command line completer
package completer

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/env"
	"github.com/xo/usql/rline"
	"github.com/xo/usql/text"
)

const (
	WORD_BREAKS = "\t\n$><=;|&{() "
)

type caseType bool

var (
	IGNORE_CASE            = caseType(true)
	MATCH_CASE             = caseType(false)
	CommonSqlStartCommands = []string{
		"ABORT",
		"ALTER",
		"ANALYZE",
		"BEGIN",
		"CALL",
		"CHECKPOINT",
		"CLOSE",
		"CLUSTER",
		"COMMENT",
		"COMMIT",
		"COPY",
		"CREATE",
		"DEALLOCATE",
		"DECLARE",
		"DELETE FROM",
		"DESC",
		"DESCRIBE",
		"DISCARD",
		"DO",
		"DROP",
		"END",
		"EXEC",
		"EXECUTE",
		"EXPLAIN",
		"FETCH",
		"GRANT",
		"IMPORT",
		"INSERT",
		"LIST",
		"LISTEN",
		"LOAD",
		"LOCK",
		"MOVE",
		"NOTIFY",
		"PRAGMA",
		"PREPARE",
		"REASSIGN",
		"REFRESH MATERIALIZED VIEW",
		"REINDEX",
		"RELEASE",
		"RESET",
		"REVOKE",
		"ROLLBACK",
		"SAVEPOINT",
		"SECURITY LABEL",
		"SELECT",
		"SET",
		"SHOW",
		"START",
		"TABLE",
		"TRUNCATE",
		"UNLISTEN",
		"UPDATE",
		"VACUUM",
		"VALUES",
		"WITH",
	}
	CommonSqlCommands = []string{
		"AND",
		"CASE",
		"CROSS JOIN",
		"ELSE",
		"END",
		"FETCH",
		"FROM",
		"FULL OUTER JOIN",
		"GROUP BY",
		"HAVING",
		"IN",
		"INNER JOIN",
		"IS NOT NULL",
		"IS NULL",
		"JOIN",
		"LEFT JOIN",
		"LIMIT",
		"NOT",
		"ON",
		"OR",
		"ORDER BY",
		"THEN",
		"WHEN",
		"WHERE",
	}
)

func NewDefaultCompleter(opts ...Option) rline.Completer {
	c := completer{
		// an empty struct satisfies the metadata.Reader interface, because it is actually empty
		reader:           struct{}{},
		logger:           log.New(os.Stderr, "ERROR: ", log.LstdFlags),
		sqlStartCommands: CommonSqlStartCommands,
		// TODO do we need to add built-in functions like, COALESCE, CAST, NULLIF, CONCAT etc?
		sqlCommands: CommonSqlCommands,
		schemaKind:  "schema",
	}
	for _, o := range opts {
		o(&c)
	}
	// return a pointer so Invalidate (pointer receiver) is reachable
	// through the AutoCompleter interface
	return &c
}

// Option to configure the reader
type Option func(*completer)

// WithDB option
func WithDB(db metadata.DB) Option {
	return func(c *completer) {
		c.db = db
	}
}

// WithReader option
func WithReader(r metadata.Reader) Option {
	return func(c *completer) {
		c.reader = r
	}
}

// WithLogger option
func WithLogger(l logger) Option {
	return func(c *completer) {
		c.logger = l
	}
}

// WithSQLStartCommands that can begin a query
func WithSQLStartCommands(commands []string) Option {
	return func(c *completer) {
		c.sqlStartCommands = commands
	}
}

// WithSQLCommands that can be any part of a query
func WithSQLCommands(commands []string) Option {
	return func(c *completer) {
		c.sqlCommands = commands
	}
}

// WithConnStrings option
func WithConnStrings(connStrings []string) Option {
	return func(c *completer) {
		c.connStrings = connStrings
	}
}

// WithAliasNames option
func WithAliasNames(names []string) Option {
	return func(c *completer) {
		c.aliasNames = names
	}
}

// WithSchemaKind sets the display kind reported for namespace (schema)
// candidates. Databases name namespaces differently — "schema" (PostgreSQL),
// "user" (Oracle, where schemas and users are the same thing) or "database"
// (MySQL) — and the menu badge says which.
func WithSchemaKind(kind string) Option {
	return func(c *completer) {
		if kind != "" {
			c.schemaKind = kind
		}
	}
}

// WithBeforeComplete option
func WithBeforeComplete(f CompleteFunc) Option {
	return func(c *completer) {
		c.beforeComplete = f
	}
}

// completer based on https://github.com/postgres/postgres/blob/9f3665fbfc34b963933e51778c7feaa8134ac885/src/bin/psql/tab-complete.c
type completer struct {
	db               metadata.DB
	reader           metadata.Reader
	logger           logger
	sqlStartCommands []string
	sqlCommands      []string
	connStrings      []string
	aliasNames       []string
	beforeComplete   CompleteFunc
	// schemaKind is the display kind of namespace candidates (see
	// WithSchemaKind).
	schemaKind string
	// cache is the metadata query cache installed by WithContextCompletion.
	cache *cachedReader
	// snap is the connect-time catalog snapshot installed by
	// WithContextCompletion, serving the hot table/function/sequence/schema
	// candidate queries from memory.
	snap *snapshotReader
}

// CompleteFunc returns patterns completing current text, using previous words as context
type CompleteFunc func(previousWords []string, text []rune) []rline.Cand

type logger interface {
	Println(...interface{})
}

// optionsSource exposes a completer's unfiltered candidate options for a
// replace-style (context) completion, so the live wrapper can memoize the
// option set once per statement context and re-filter it client-side as the
// word grows — zero completer work per keystroke. Implemented by *completer
// when the context path is installed.
type optionsSource interface {
	optionsFor(line []rune, pos int) (opts []rline.Cand, word string, ok bool)
}

// wordStart returns the index of the first rune of the word at pos.
func wordStart(line []rune, pos int) int {
	if pos > len(line) {
		pos = len(line)
	}
	i := pos
	for i > 0 && !strings.ContainsRune(WORD_BREAKS, line[i-1]) {
		i--
	}
	return i
}

// optionsFor returns the unfiltered candidate set for the context path at
// pos, plus the word being typed. Ordered results (VALUES field hints) and
// non-context paths (meta-command arguments, heuristics) are not memoizable
// and return ok=false.
func (c completer) optionsFor(line []rune, pos int) ([]rline.Cand, string, bool) {
	if pos > len(line) {
		pos = len(line)
	}
	text := line[wordStart(line, pos):pos]
	if len(text) == 0 || text[0] == '\\' {
		return nil, "", false
	}
	ctx := parseContext(line, pos)
	opts, ok, ordered := c.contextOptions(ctx)
	if !ok || ordered {
		return nil, "", false
	}
	return opts, ctx.Qualifier + ctx.Object, true
}

func (c completer) Do(line []rune, start int) ([]rline.Cand, int) {
	// a terminator ends the statement: complete nothing while no new word is
	// typed, and complete the new statement as if typed on its own line
	if k := rline.LastStatementStart(line, start); k > 0 {
		if rline.AtStatementEnd(line, start) {
			return nil, 0
		}
		line, start = line[k:], start-k
	}
	i := wordStart(line, start)
	previousWords := getPreviousWords(start, line)
	text := line[i:start]

	if c.beforeComplete != nil {
		result := c.beforeComplete(previousWords, text)
		if result != nil {
			return result, len(text)
		}
	}
	result := c.complete(previousWords, text)
	if result != nil {
		return result, len(text)
	}
	return nil, 0
}

func (c completer) complete(previousWords []string, text []rune) []rline.Cand {
	if len(text) > 0 {
		if len(previousWords) == 0 && text[0] == '\\' {
			/* If current word is a backslash command, offer completions for that */
			return CompleteFromListKind("command", MATCH_CASE, text, backslashCommands...)
		}
		if text[0] == ':' {
			if len(text) == 1 || text[1] == ':' {
				return nil
			}
			/* If current word is a variable interpolation, handle that case */
			if text[1] == '\'' {
				return completeFromVariables(text, ":'", "'", true)
			}
			if text[1] == '"' {
				return completeFromVariables(text, ":\"", "\"", true)
			}
			return completeFromVariables(text, ":", "", true)
		}
	}
	// meta-command argument positions: the policy table decides what, if
	// anything, each argument of each command completes as; unknown
	// commands and exhausted positions complete nothing — never SQL keywords
	if n := len(previousWords); n > 0 && strings.HasPrefix(previousWords[n-1], "\\") {
		return c.completeMetaArg(previousWords[n-1], n-1, previousWords, text)
	}
	if len(previousWords) == 0 {
		/* If no previous word, suggest one of the basic sql commands */
		return CompleteFromList(text, c.sqlStartCommands...)
	}
	/* DELETE --- can be inside EXPLAIN, RULE, etc */
	/* ... despite which, only complete DELETE with FROM at start of line */
	if matches(IGNORE_CASE, previousWords, "DELETE") {
		return CompleteFromList(text, "FROM")
	}
	/* Complete DELETE FROM with a list of tables */
	if TailMatches(IGNORE_CASE, previousWords, "DELETE", "FROM") {
		return c.completeWithUpdatables(text)
	}
	/* Complete DELETE FROM <table> */
	if TailMatches(IGNORE_CASE, previousWords, "DELETE", "FROM", "*") {
		return CompleteFromList(text, "USING", "WHERE")
	}
	/* XXX: implement tab completion for DELETE ... USING */

	/* Complete CREATE */
	if TailMatches(IGNORE_CASE, previousWords, "CREATE") {
		return CompleteFromList(text, "DATABASE", "SCHEMA", "SEQUENCE", "TABLE", "VIEW", "TEMPORARY")
	}
	if TailMatches(IGNORE_CASE, previousWords, "CREATE", "TEMP|TEMPORARY") {
		return CompleteFromList(text, "TABLE", "VIEW")
	}
	if TailMatches(IGNORE_CASE, previousWords, "CREATE", "TABLE", "*") || TailMatches(IGNORE_CASE, previousWords, "CREATE", "TEMP|TEMPORARY", "TABLE", "*") {
		return CompleteFromList(text, "(")
	}
	/* INSERT --- can be inside EXPLAIN, RULE, etc */
	/* Complete INSERT with "INTO" */
	if TailMatches(IGNORE_CASE, previousWords, "INSERT") {
		return CompleteFromList(text, "INTO")
	}
	/* Complete INSERT INTO with table names */
	if TailMatches(IGNORE_CASE, previousWords, "INSERT", "INTO") {
		return c.completeWithUpdatables(text)
	}
	/* Complete "INSERT INTO <table> (" with attribute names */
	if TailMatches(IGNORE_CASE, previousWords, "INSERT", "INTO", "*", "(") {
		return c.completeWithAttributes(IGNORE_CASE, previousWords[1], text)
	}

	/*
	 * Complete INSERT INTO <table> with "(" or "VALUES" or "SELECT" or
	 * "TABLE" or "DEFAULT VALUES" or "OVERRIDING"
	 */
	if TailMatches(IGNORE_CASE, previousWords, "INSERT", "INTO", "*") {
		return CompleteFromList(text, "(", "DEFAULT VALUES", "SELECT", "TABLE", "VALUES", "OVERRIDING")
	}

	/*
	 * Complete INSERT INTO <table> (attribs) with "VALUES" or "SELECT" or
	 * "TABLE" or "OVERRIDING"
	 */
	if TailMatches(IGNORE_CASE, previousWords, "INSERT", "INTO", "*", "*") &&
		strings.HasSuffix(previousWords[0], ")") {
		return CompleteFromList(text, "SELECT", "TABLE", "VALUES", "OVERRIDING")
	}

	/* Complete OVERRIDING */
	if TailMatches(IGNORE_CASE, previousWords, "OVERRIDING") {
		return CompleteFromList(text, "SYSTEM VALUE", "USER VALUE")
	}

	/* Complete after OVERRIDING clause */
	if TailMatches(IGNORE_CASE, previousWords, "OVERRIDING", "*", "VALUE") {
		return CompleteFromList(text, "SELECT", "TABLE", "VALUES")
	}

	/* Insert an open parenthesis after "VALUES" */
	if TailMatches(IGNORE_CASE, previousWords, "VALUES") && !TailMatches(IGNORE_CASE, previousWords, "DEFAULT", "VALUES") {
		return CompleteFromList(text, "(")
	}
	/* UPDATE --- can be inside EXPLAIN, RULE, etc */
	/* If prev. word is UPDATE suggest a list of tables */
	if TailMatches(IGNORE_CASE, previousWords, "UPDATE") {
		return c.completeWithUpdatables(text)
	}
	/* Complete UPDATE <table> with "SET" */
	if TailMatches(IGNORE_CASE, previousWords, "UPDATE", "*") {
		return CompleteFromList(text, "SET")
	}
	/* Complete UPDATE <table> SET with list of attributes */
	if TailMatches(IGNORE_CASE, previousWords, "UPDATE", "*", "SET") {
		return c.completeWithAttributes(IGNORE_CASE, previousWords[1], text)
	}
	/* UPDATE <table> SET <attr> = */
	if TailMatches(IGNORE_CASE, previousWords, "UPDATE", "*", "SET", "!*=") {
		return CompleteFromList(text, "=")
	}
	/* WHERE */
	/* Simple case of the word before the where being the table name */
	if TailMatches(IGNORE_CASE, previousWords, "*", "WHERE") {
		// TODO would be great to _try_ to parse the (incomplete) query
		// and get a list of possible selectables to filter by
		return c.completeWithAttributes(IGNORE_CASE, previousWords[1], text,
			"AND",
			"OR",
			"CASE",
			"WHEN",
			"THEN",
			"ELSE",
			"END",
		)
	}

	/* ... FROM | JOIN ... */
	if TailMatches(IGNORE_CASE, previousWords, "FROM|JOIN") {
		return c.completeWithSelectables(text)
	}
	/* TABLE, but not TABLE embedded in other commands */
	if matches(IGNORE_CASE, previousWords, "TABLE") {
		return c.completeWithUpdatables(text)
	}
	// is suggesting basic sql commands better than nothing?
	return CompleteFromList(text, c.sqlCommands...)
}

func getPreviousWords(point int, buf []rune) []string {
	var i int

	/*
	 * Allocate a slice of strings (rune slices). The worst case is that the line contains only
	 * non-whitespace WORD_BREAKS characters, making each one a separate word.
	 * This is usually much more space than we need, but it's cheaper than
	 * doing a separate malloc() for each word.
	 */
	previousWords := make([]string, 0, point*2)

	/*
	 * First we look for a non-word char before the current point.  (This is
	 * probably useless, if readline is on the same page as we are about what
	 * is a word, but if so it's cheap.)
	 */
	for i = point - 1; i >= 0; i-- {
		if strings.ContainsRune(WORD_BREAKS, buf[i]) {
			break
		}
	}
	point = i

	/*
	 * Now parse words, working backwards, until we hit start of line.  The
	 * backwards scan has some interesting but intentional properties
	 * concerning parenthesis handling.
	 */
	for point >= 0 {
		var start, end int
		inquotes := false
		parentheses := 0

		/* now find the first non-space which then constitutes the end */
		end = -1
		for i = point; i >= 0; i-- {
			if !unicode.IsSpace(buf[i]) {
				end = i
				break
			}
		}
		/* if no end found, we're done */
		if end < 0 {
			break
		}

		/*
		 * Otherwise we now look for the start.  The start is either the last
		 * character before any word-break character going backwards from the
		 * end, or it's simply character 0.  We also handle open quotes and
		 * parentheses. Single-quoted string literals are folded into one
		 * opaque word, so their contents never count as SQL keywords (the
		 * context path's tokenizer does the same).
		 */
		var inSingle bool
		for start = end; start > 0; start-- {
			if buf[start] == '"' {
				inquotes = !inquotes
			} else if buf[start] == '\'' {
				inSingle = !inSingle
			}
			if inquotes || inSingle {
				continue
			}
			if buf[start] == ')' {
				parentheses++
			} else if buf[start] == '(' {
				parentheses -= 1
				if parentheses <= 0 {
					break
				}
			} else if parentheses == 0 && strings.ContainsRune(WORD_BREAKS, buf[start-1]) {
				break
			}
		}

		/* Return the word located at start to end inclusive */
		i = end - start + 1
		previousWords = append(previousWords, string(buf[start:start+i]))

		/* Continue searching */
		point = start - 1
	}

	return previousWords
}

// TailMatches when last words match all patterns
func TailMatches(ct caseType, words []string, patterns ...string) bool {
	if len(words) < len(patterns) {
		return false
	}
	for i, p := range patterns {
		if !wordMatches(ct, p, words[len(patterns)-i-1]) {
			return false
		}
	}
	return true
}

func matches(ct caseType, words []string, patterns ...string) bool {
	if len(words) != len(patterns) {
		return false
	}
	for i, p := range patterns {
		if !wordMatches(ct, p, words[len(patterns)-i-1]) {
			return false
		}
	}
	return true
}

func wordMatches(ct caseType, pattern, word string) bool {
	if pattern == "*" {
		return true
	}

	if pattern[0] == '!' {
		return !wordMatches(ct, pattern[1:], word)
	}

	cmp := func(a, b string) bool { return a == b }
	if ct == IGNORE_CASE {
		cmp = strings.EqualFold
	}

	for _, p := range strings.Split(pattern, "|") {
		star := strings.IndexByte(p, '*')
		if star == -1 {
			if cmp(p, word) {
				return true
			}
		} else {
			if len(word) >= len(p)-1 && cmp(p[0:star], word[0:star]) && (star >= len(p) || cmp(p[star+1:], word[len(word)-len(p)+star+1:])) {
				return true
			}
		}
	}

	return false
}

// CompleteFromList where items starts with text, ignoring case
func CompleteFromList(text []rune, options ...string) []rline.Cand {
	return CompleteFromListKind("", IGNORE_CASE, text, options...)
}

// CompleteFromListKind is CompleteFromListCase with the case type explicit.
func CompleteFromListKind(kind string, ct caseType, text []rune, options ...string) []rline.Cand {
	return CompleteFromListCase(kind, ct, text, options...)
}

// CompleteFromListCase where items starts with text
func CompleteFromListCase(kind string, ct caseType, text []rune, options ...string) []rline.Cand {
	if len(options) == 0 {
		return nil
	}
	isLower := false
	if len(text) > 0 {
		isLower = unicode.IsLower(text[0])
	}
	prefix := string(text)
	if ct == IGNORE_CASE {
		prefix = strings.ToUpper(prefix)
	}
	result := make([]rline.Cand, 0, len(options))
	for _, o := range options {
		if (ct == IGNORE_CASE && !strings.HasPrefix(strings.ToUpper(o), prefix)) ||
			(ct == MATCH_CASE && !strings.HasPrefix(o, prefix)) {
			continue
		}
		match := o[len(text):]
		if ct == IGNORE_CASE && isLower {
			match = strings.ToLower(match)
		}
		result = append(result, rline.Cand{Text: match, Kind: kind})
	}
	return result
}

func completeFromVariables(text []rune, prefix, suffix string, needValue bool) []rline.Cand {
	vars := env.Vars().Vars()
	names := make([]string, 0, len(vars))
	for name, value := range vars {
		if needValue && value == "" {
			continue
		}
		names = append(names, prefix+name+suffix)
	}
	return CompleteFromListKind("variable", MATCH_CASE, text, names...)
}

// typeKind maps a metadata table type to its display kind.
func typeKind(t string) string {
	switch strings.ToUpper(t) {
	case "TABLE", "BASE TABLE", "SYSTEM TABLE", "LOCAL TEMPORARY", "GLOBAL TEMPORARY":
		return "table"
	case "VIEW", "SYSTEM VIEW":
		return "view"
	case "MATERIALIZED VIEW":
		return "matview"
	case "SYNONYM":
		return "synonym"
	case "SEQUENCE":
		return "sequence"
	}
	return ""
}

// sortCands orders candidates by text, keeping kinds attached.
func sortCands(cands []rline.Cand) {
	sort.Slice(cands, func(i, j int) bool { return cands[i].Text < cands[j].Text })
}

func (c completer) completeWithSelectables(text []rune) []rline.Cand {
	filter := parseIdentifier(string(text))
	names := c.getNamespaces(filter)
	if r, ok := c.reader.(metadata.TableReader); ok {
		tables := c.getCands(
			func() (iterator, error) {
				return r.Tables(filter)
			},
			func(res interface{}) (string, string) {
				t := res.(*metadata.TableSet).Get()
				return qualifiedIdentifier(filter, t.Catalog, t.Schema, t.Name), typeKind(t.Type)
			},
		)
		names = append(names, tables...)
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok {
		functions := c.getCands(
			func() (iterator, error) {
				return r.Functions(filter)
			},
			func(res interface{}) (string, string) {
				f := res.(*metadata.FunctionSet).Get()
				return qualifiedIdentifier(filter, f.Catalog, f.Schema, f.Name), "function"
			},
		)
		names = append(names, functions...)
	}
	if r, ok := c.reader.(metadata.SequenceReader); ok {
		sequences := c.getCands(
			func() (iterator, error) {
				return r.Sequences(filter)
			},
			func(res interface{}) (string, string) {
				s := res.(*metadata.SequenceSet).Get()
				return qualifiedIdentifier(filter, s.Catalog, s.Schema, s.Name), "sequence"
			},
		)
		names = append(names, sequences...)
	}
	sortCands(names)
	// TODO make sure CompleteFromList would properly handle quoted identifiers
	return CompleteFromListCands(text, names)
}

func (c completer) completeWithUpdatables(text []rune) []rline.Cand {
	filter := parseIdentifier(string(text))
	names := c.getNamespaces(filter)
	if r, ok := c.reader.(metadata.TableReader); ok {
		// exclude materialized views, sequences, system tables, synonyms
		filter.Types = []string{"TABLE", "BASE TABLE", "LOCAL TEMPORARY", "GLOBAL TEMPORARY", "VIEW"}
		tables := c.getCands(
			func() (iterator, error) {
				return r.Tables(filter)
			},
			func(res interface{}) (string, string) {
				t := res.(*metadata.TableSet).Get()
				return qualifiedIdentifier(filter, t.Catalog, t.Schema, t.Name), typeKind(t.Type)
			},
		)
		names = append(names, tables...)
	}
	sortCands(names)
	// TODO make sure CompleteFromList would properly handle quoted identifiers
	return CompleteFromListCands(text, names)
}

func (c completer) getNamespaces(f metadata.Filter) []rline.Cand {
	names := make([]rline.Cand, 0, 10)
	// Catalogs (database names) are not offered as namespaces: in FROM/JOIN/
	// UPDATE/INSERT INTO positions one picks schema-qualified objects, and
	// for PostgreSQL-style databases the catalog list is the database list,
	// which psql does not offer either. \l completes catalogs via
	// completeWithCatalogs.
	if f.Catalog != "" {
		// filter is already fully qualified, so don't return any namespaces
		return names
	}
	if r, ok := c.reader.(metadata.SchemaReader); ok {
		schemas := c.getCands(
			func() (iterator, error) {
				if f.Schema != "" {
					// name should already have a wildcard appended
					return r.Schemas(metadata.Filter{Catalog: f.Schema, Name: f.Name, WithSystem: true})
				}
				return r.Schemas(f)
			},
			func(res interface{}) (string, string) {
				s := res.(*metadata.SchemaSet).Get()
				return qualifiedIdentifier(f, "", s.Catalog, s.Schema), c.schemaKind
			},
		)
		names = append(names, schemas...)
	}
	return names
}

func (c completer) completeWithAttributes(_ caseType, selectable string, text []rune, options ...string) []rline.Cand {
	names := make([]rline.Cand, 0, 10)
	if r, ok := c.reader.(metadata.ColumnReader); ok {
		parent := parseParentIdentifier(selectable)
		columns := c.getCands(
			func() (iterator, error) {
				return r.Columns(parent)
			},
			func(res interface{}) (string, string) {
				return res.(*metadata.ColumnSet).Get().Name, "column"
			},
		)
		names = append(names, columns...)
	}
	if r, ok := c.reader.(metadata.FunctionReader); ok {
		filter := parseIdentifier(string(text))
		// functions don't have to be fully qualified to be callable
		filter.OnlyVisible = false
		functions := c.getCands(
			func() (iterator, error) {
				return r.Functions(filter)
			},
			func(res interface{}) (string, string) {
				return res.(*metadata.FunctionSet).Get().Name, "function"
			},
		)
		names = append(names, functions...)
	}
	names = append(names, CompleteFromList(text, options...)...)
	return CompleteFromListCands(text, names)
}

// parseIdentifier into catalog, schema and name
func parseIdentifier(name string) metadata.Filter {
	// TODO handle quoted identifiers
	result := metadata.Filter{}
	if !strings.ContainsRune(name, '.') {
		result.Name = name + "%"
		result.OnlyVisible = true
	} else {
		parts := strings.SplitN(name, ".", 3)
		if len(parts) == 2 {
			result.Schema = parts[0]
			result.Name = parts[1] + "%"
		} else {
			result.Catalog = parts[0]
			result.Schema = parts[1]
			result.Name = parts[2] + "%"
		}
	}

	if result.Schema != "" || len(result.Name) > 3 {
		result.WithSystem = true
	}
	return result
}

// parseParentIdentifier into catalog, schema and parent
func parseParentIdentifier(name string) metadata.Filter {
	// TODO handle quoted identifiers
	result := metadata.Filter{}
	if !strings.ContainsRune(name, '.') {
		result.Parent = name
		result.OnlyVisible = true
	} else {
		parts := strings.SplitN(name, ".", 3)
		if len(parts) == 2 {
			result.Schema = parts[0]
			result.Parent = parts[1]
		} else {
			result.Catalog = parts[0]
			result.Schema = parts[1]
			result.Parent = parts[2]
		}
	}

	if result.Schema != "" {
		result.WithSystem = true
	}
	return result
}

// fullIdentifier renders an object's name with all known namespace parts,
// independent of what the user typed — schema.table or
// catalog.schema.table. The pseudo-catalog "def" (MySQL's implicit
// catalog) is ignored.
func fullIdentifier(catalog, schema, name string) string {
	switch {
	case catalog != "" && catalog != "def" && schema != "":
		return catalog + "." + schema + "." + name
	case schema != "":
		return schema + "." + name
	default:
		return name
	}
}

func qualifiedIdentifier(filter metadata.Filter, catalog, schema, name string) string {
	// TODO handle quoted identifiers
	if filter.Catalog != "" && filter.Schema != "" {
		return catalog + "." + schema + "." + name
	}
	if filter.Schema != "" {
		return schema + "." + name
	}
	return name
}

// CompleteFromListCands filters already-built candidates by the typed text
// (case-insensitive prefix), returning the append-style suffix after the
// typed text, keeping kinds. nil options decline (callers fall through);
// non-nil options with no matches yield an empty result — nothing to
// suggest, but the path was authoritative.
func CompleteFromListCands(text []rune, options []rline.Cand) []rline.Cand {
	if options == nil {
		return nil
	}
	prefix := strings.ToUpper(string(text))
	isLower := len(text) > 0 && unicode.IsLower(text[0])
	result := make([]rline.Cand, 0, len(options))
	for _, o := range options {
		if !strings.HasPrefix(strings.ToUpper(o.Text), prefix) {
			continue
		}
		match := o.Text[len(text):]
		if isLower {
			match = strings.ToLower(match)
		}
		result = append(result, rline.Cand{Text: match, Kind: o.Kind})
	}
	return result
}

func (c completer) getNames(query func() (iterator, error), mapper func(interface{}) string) []string {
	res, err := query()
	if err != nil {
		if err != text.ErrNotSupported {
			c.logger.Println("Error getting selectables", err)
		}
		return nil
	}
	defer res.Close()

	// there can be duplicates if names are not qualified
	values := make(map[string]struct{}, 10)
	for res.Next() {
		values[mapper(res)] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for v := range values {
		result = append(result, v)
	}
	return result
}

// getCands is getNames with a display kind per row, deduplicated by text.
func (c completer) getCands(query func() (iterator, error), mapper func(interface{}) (string, string)) []rline.Cand {
	res, err := query()
	if err != nil {
		if err != text.ErrNotSupported {
			c.logger.Println("Error getting selectables", err)
		}
		return nil
	}
	defer res.Close()

	seen := make(map[string]struct{}, 10)
	var result []rline.Cand
	for res.Next() {
		name, kind := mapper(res)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, rline.Cand{Text: name, Kind: kind})
	}
	return result
}

type iterator interface {
	Next() bool
	Close() error
}

func completeFromFiles(text []rune) []rline.Cand {
	// TODO handle quotes properly
	dir := filepath.Dir(string(text))
	dirs, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	matches := make([]string, 0, len(dirs))
	switch dir {
	case ".":
		dir = ""
	case "/":
		// pass
	default:
		dir += "/"
	}
	for _, entry := range dirs {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		matches = append(matches, dir+name)
	}
	out := make([]rline.Cand, 0, len(matches))
	for _, m := range matches {
		out = append(out, rline.Cand{Text: m, Kind: "file"})
	}
	return out
}
