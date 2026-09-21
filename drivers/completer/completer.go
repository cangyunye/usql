// completer package provides a generic SQL command line completer
package completer

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/env"
	"github.com/xo/usql/rline"
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
		"SELECT",
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
		sqlCommands:    CommonSqlCommands,
		schemaKind:     "schema",
		namesVerbs:     map[string]bool{},
		statementWords: map[string][]string{},
	}
	if statementStartsUse(c.sqlStartCommands) {
		c.namesVerbs["USE"] = true
	}
	for _, o := range opts {
		o(&c)
	}
	// return a pointer so Invalidate (pointer receiver) is reachable
	// through the AutoCompleter interface
	return &c
}

// statementStartsUse reports whether the dialect registers USE (MySQL-family
// "USE <database>") as a statement-starting command.
func statementStartsUse(commands []string) bool {
	for _, w := range commands {
		if strings.EqualFold(w, "USE") {
			return true
		}
	}
	return false
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
		if statementStartsUse(commands) {
			c.namesVerbs["USE"] = true
		}
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

// WithStatementWords registers statement verbs whose next word is
// dialect-fixed, mapped to the words to offer: SQLite's "PRAGMA <name>"
// positions (the pragma names) and "ATTACH <DATABASE>" are the canonical
// uses. Candidates are served only immediately after the verb — a cursor
// later in the statement ("PRAGMA journal_mode =") falls back to keywords.
// Verb keys are matched case-insensitively; word lists are offered as typed.
func WithStatementWords(words map[string][]string) Option {
	return func(c *completer) {
		for verb, list := range words {
			c.statementWords[strings.ToUpper(verb)] = list
		}
	}
}

// WithNamesVerbs registers statement verbs whose following identifier is a
// namespace name, so the word after the verb completes from the L1 schema
// cache ("DETACH <attached-schema>"; USE is auto-registered from
// sqlStartCommands for the MySQL family).
func WithNamesVerbs(verbs ...string) Option {
	return func(c *completer) {
		for _, v := range verbs {
			c.namesVerbs[strings.ToUpper(v)] = true
		}
	}
}

// BuiltinFunc is one static function completion: args is the signature shown
// as dim detail; bare functions take no parentheses. See WithTableFunctions.
type BuiltinFunc = builtinFunc

// WithTableFunctions registers the dialect's set-returning functions —
// functions valid in FROM/JOIN positions ("SELECT * FROM pragma_table_info(
// ..."). They are offered in the table object tier alongside schemas and
// relations, and excluded where only relations are valid (INSERT INTO,
// UPDATE).
func WithTableFunctions(funcs ...BuiltinFunc) Option {
	return func(c *completer) {
		c.tableFunctions = append(c.tableFunctions, funcs...)
	}
}

// WithExtraSQLCommands appends dialect keywords to the mid-statement keyword
// fallback, for words the common list lacks ("GLOB", "REGEXP" on SQLite).
func WithExtraSQLCommands(words ...string) Option {
	return func(c *completer) {
		c.sqlCommands = append(c.sqlCommands, words...)
	}
}

// completer completes SQL statements from the statement's clause context and
// the database's catalog, and backslash commands from the declarative
// meta-argument policy table.
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
	// cache is the lazy catalog cache installed by WithContextCompletion.
	cache *catalogCache
	// caps remembers which catalog kinds the database does not serve
	// (MySQL has no sequences), so failed loads are never re-issued.
	caps *catalogCaps
	// namesVerbs are the statement verbs whose following identifier is a
	// namespace name — "USE <database>", "DETACH <schema>" — completed from
	// the L1 schema cache. USE is seeded from sqlStartCommands; drivers add
	// others via WithNamesVerbs.
	namesVerbs map[string]bool
	// statementWords are the statement verbs whose next word is
	// dialect-fixed — PRAGMA's pragma names, ATTACH's DATABASE — served in
	// the AfterVerb position (see WithStatementWords).
	statementWords map[string][]string
	// tableFunctions are the dialect's set-returning functions, offered in
	// FROM/JOIN object positions alongside schema objects (see
	// WithTableFunctions).
	tableFunctions []builtinFunc
}

// CompleteFunc returns patterns completing current text, using previous words as context
type CompleteFunc func(previousWords []string, text []rune) []rline.Cand

type logger interface {
	Println(...interface{})
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

// Do completes append-style: backslash commands, variable interpolations and
// meta-command arguments (as suffixes after the typed text), the driver hook
// (e.g. MySQL USE databases), and — when nothing else applies — SQL
// keywords, which cost no queries. Object completion (tables, columns) is
// replace-style and lives in DoRepl; the readline layer tries that first.
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

	if res := c.completeMeta(previousWords, text); res != nil {
		return res, len(text)
	}
	// namespace-name positions ("USE <database>", "DETACH <schema>"):
	// namespaces from the L1 schema cache. The old driver hook
	// (WithBeforeComplete) queried the reader synchronously on the UI path —
	// up to the reader timeout per Tab.
	if n := len(previousWords); n > 0 && c.namesVerbs[strings.ToUpper(previousWords[n-1])] {
		if objs, ok := c.schemas(); ok {
			return completeFromListCands(text, candsObjs(objs)), len(text)
		}
		// still loading: decline, the kick re-renders when it lands
		return nil, len(text)
	}
	if c.beforeComplete != nil {
		if res := c.beforeComplete(previousWords, text); res != nil {
			return res, len(text)
		}
	}
	if len(previousWords) == 0 {
		// at a statement start, offer the statement keywords
		return completeFromList(text, c.sqlStartCommands...), len(text)
	}
	// mid-statement: the common clause keywords, never a query
	return completeFromList(text, c.sqlCommands...), len(text)
}

// completeMeta completes the meta layer: a backslash command itself, a
// variable interpolation, or a meta-command's argument via the declarative
// policy table. Candidates are append-style suffixes; nil declines.
func (c completer) completeMeta(previousWords []string, text []rune) []rline.Cand {
	if len(text) > 0 {
		if len(previousWords) == 0 && text[0] == '\\' {
			// a backslash command: offer the command set
			return completeFromListKind("command", MATCH_CASE, text, backslashCommands...)
		}
		if text[0] == ':' {
			if len(text) == 1 || text[1] == ':' {
				return nil
			}
			// a variable interpolation
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
	return nil
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

// completeFromList where items starts with text, ignoring case
func completeFromList(text []rune, options ...string) []rline.Cand {
	return completeFromListKind("", IGNORE_CASE, text, options...)
}

// completeFromListKind is completeFromListCase with the case type explicit.
func completeFromListKind(kind string, ct caseType, text []rune, options ...string) []rline.Cand {
	return completeFromListCase(kind, ct, text, options...)
}

// completeFromListCase where items starts with text
func completeFromListCase(kind string, ct caseType, text []rune, options ...string) []rline.Cand {
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
	return completeFromListKind("variable", MATCH_CASE, text, names...)
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

// completeFromListCands filters already-built candidates by the typed text
// (case-insensitive prefix), returning the append-style suffix after the
// typed text, keeping kinds. nil options decline (callers fall through);
// non-nil options with no matches yield an empty result — nothing to
// suggest, but the path was authoritative.
func completeFromListCands(text []rune, options []rline.Cand) []rline.Cand {
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
		result = append(result, rline.Cand{Text: match, Kind: o.Kind, Detail: o.Detail})
	}
	return result
}

// completeFromFiles suggests file-system paths for arguments like \i.
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
