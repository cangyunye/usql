package completer

import (
	"strings"
	"unicode"
)

// TableRef is a possibly catalog/schema-qualified table reference seen in
// FROM, JOIN, UPDATE or INSERT INTO clauses.
type TableRef struct {
	Catalog string
	Schema  string
	Name    string
}

// Context describes the SQL completion context at a cursor position, derived
// by a lightweight clause scan of the statement typed so far. Unlike the
// tail-matching heuristics in completer.go, it tracks the tables and aliases
// introduced by the statement, so candidates can be generated from the
// objects actually in scope.
type Context struct {
	// Clause is the innermost clause keyword in effect at the cursor,
	// upper-cased: SELECT, FROM, JOIN, ON, WHERE, GROUP, ORDER, HAVING, SET,
	// INTO, VALUES or USING. Empty when no clause was recognized.
	Clause string
	// Qualifier is the dotted prefix of the word at the cursor, including
	// its trailing dot ("f.", "public.", "remote.default."). Empty when the
	// word is unqualified.
	Qualifier string
	// Object is the partial identifier being typed: the word segment after
	// the last dot, or the whole word when unqualified.
	Object string
	// AfterParen reports whether the last non-space character before the
	// cursor is '(' — e.g. the column list of INSERT INTO, or function
	// arguments.
	AfterParen bool
	// OpenParens is the number of '(' before the cursor word that are not
	// yet closed — the cursor is inside a column list, function call or
	// subquery.
	OpenParens int
	// Tables lists the table references in scope, in order of appearance.
	// Derived tables and CTE bodies only appear when the cursor is inside
	// them.
	Tables []TableRef
	// TableListed reports whether a table reference was seen after the
	// current clause keyword — INSERT INTO film <cursor> lists one, while
	// FROM film JOIN <cursor> does not for the JOIN.
	TableListed bool
	// IntoListDone reports whether a complete "(a, b)" column group was
	// written after the INSERT INTO target table.
	IntoListDone bool
	// Aliases maps each alias (and each table's own name) to its reference.
	// The alias of a derived table maps to the zero TableRef, because its
	// columns cannot be resolved without evaluating the subquery.
	Aliases map[string]TableRef
}

// parseContext scans line[:start] and derives the completion context at the
// cursor. It never queries the database, so it is safe to call on every
// completion request.
func parseContext(line []rune, start int) Context {
	if start > len(line) {
		start = len(line)
	}
	// The word at the cursor extends back over word characters (identifiers
	// and dots) to the first space or other break — exactly the span the
	// readline completer replaces. It must be scanned from the raw line,
	// because whitespace does not survive tokenization.
	ws := start
	for ws > 0 && isWordChar(line[ws-1]) {
		ws--
	}
	word := string(line[ws:start])

	// The clause scan sees only the tokens before the word, so a partial
	// identifier never influences the detected clause.
	tokens := tokenize(line, ws)
	ctx := scanClauses(tokens)
	ctx.Qualifier, ctx.Object = splitWord(word)
	ctx.AfterParen = afterOpenParen(line, start)
	ctx.OpenParens = openParens(tokens)
	return ctx
}

// scanClauses walks the token stream, tracking the innermost clause keyword
// and collecting table references and aliases from table lists (FROM, JOIN,
// UPDATE, INSERT INTO). Complete parenthesized groups are skipped, so
// subqueries do not clobber the outer scope; incomplete groups are scanned,
// because the cursor being inside one means it is the active scope.
func scanClauses(tokens []token) Context {
	ctx := Context{Aliases: map[string]TableRef{}}
	match := matchParens(tokens)
	tableMode := false
	derivedTable := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.kind == tokLparen {
			if j, ok := match[i]; ok {
				i = j
				// a complete group in a table list is a derived table, and
				// the identifier that follows is its alias
				derivedTable = tableMode
				if ctx.Clause == "INTO" {
					ctx.IntoListDone = true
				}
			}
			continue
		}
		if t.kind == tokSemicolon {
			// a new statement starts with an empty scope
			ctx = Context{Aliases: map[string]TableRef{}}
			tableMode, derivedTable = false, false
			continue
		}
		if t.kind != tokIdent {
			continue
		}
		up := strings.ToUpper(t.text)
		if clause, ok := contextClauses[up]; ok {
			ctx.Clause = clause
			ctx.TableListed = false
			tableMode = tableClauses[clause]
			derivedTable = false
			continue
		}
		if !tableMode || reservedWords[up] {
			continue
		}
		if derivedTable {
			ctx.Aliases[t.text] = TableRef{}
			ctx.TableListed = true
			derivedTable = false
			continue
		}
		ref, next := parseTableRef(tokens, i)
		if next < len(tokens) && tokens[next].kind == tokIdent {
			switch {
			case strings.EqualFold(tokens[next].text, "AS") && next+1 < len(tokens) && tokens[next+1].kind == tokIdent:
				ctx.Aliases[tokens[next+1].text] = ref
				next += 2
			case !reservedWords[strings.ToUpper(tokens[next].text)]:
				ctx.Aliases[tokens[next].text] = ref
				next++
			}
		}
		ctx.Aliases[ref.Name] = ref
		ctx.Tables = append(ctx.Tables, ref)
		ctx.TableListed = true
		i = next - 1
	}
	return ctx
}

// parseTableRef reads a possibly qualified table reference starting at the
// identifier token i, returning the reference and the index of the first
// token after it.
func parseTableRef(tokens []token, i int) (TableRef, int) {
	var parts []string
	for i < len(tokens) && tokens[i].kind == tokIdent {
		parts = append(parts, tokens[i].text)
		i++
		if i < len(tokens) && tokens[i].kind == tokDot {
			i++
		} else {
			break
		}
	}
	var ref TableRef
	switch {
	case len(parts) >= 3:
		ref.Catalog, ref.Schema, ref.Name = parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	case len(parts) == 2:
		ref.Schema, ref.Name = parts[0], parts[1]
	case len(parts) == 1:
		ref.Name = parts[0]
	}
	return ref, i
}

// matchParens maps the index of every '(' that has a matching ')' before the
// end of the token stream to the index of its ')'.
func matchParens(tokens []token) map[int]int {
	match := make(map[int]int)
	stack := make([]int, 0, 8)
	for i, t := range tokens {
		switch t.kind {
		case tokLparen:
			stack = append(stack, i)
		case tokRparen:
			if n := len(stack); n > 0 {
				match[stack[n-1]] = i
				stack = stack[:n-1]
			}
		}
	}
	return match
}

// openParens counts '(' tokens that have no matching ')' before the cursor
// word.
func openParens(tokens []token) int {
	depth := 0
	for _, t := range tokens {
		switch t.kind {
		case tokLparen:
			depth++
		case tokRparen:
			if depth > 0 {
				depth--
			}
		}
	}
	return depth
}

// splitWord splits a possibly qualified identifier into its dotted qualifier
// (including the trailing dot) and the object being typed.
func splitWord(word string) (qualifier, object string) {
	if i := strings.LastIndexByte(word, '.'); i >= 0 {
		return word[:i+1], word[i+1:]
	}
	return "", word
}

// afterOpenParen reports whether the last non-space rune before start is '('.
func afterOpenParen(line []rune, start int) bool {
	for i := start - 1; i >= 0; i-- {
		if !unicode.IsSpace(line[i]) {
			return line[i] == '('
		}
	}
	return false
}

// contextClauses maps clause keywords to the clause they set.
var contextClauses = map[string]string{
	"SELECT": "SELECT",
	"FROM":   "FROM",
	"JOIN":   "JOIN",
	"ON":     "ON",
	"WHERE":  "WHERE",
	"GROUP":  "GROUP",
	"ORDER":  "ORDER",
	"HAVING": "HAVING",
	"SET":    "SET",
	"INTO":   "INTO",
	"VALUES": "VALUES",
	"USING":  "USING",
	"UPDATE": "UPDATE",
}

// tableClauses are clauses that introduce a table list.
var tableClauses = map[string]bool{
	"FROM":   true,
	"JOIN":   true,
	"UPDATE": true,
	"INTO":   true,
}

// reservedWords are keywords that can never be table aliases.
var reservedWords = map[string]bool{
	"ALL": true, "AND": true, "ANY": true, "AS": true, "ASC": true,
	"BETWEEN": true, "BY": true, "CASE": true, "CROSS": true, "DELETE": true,
	"DESC": true, "DISTINCT": true, "ELSE": true, "END": true, "EXCEPT": true,
	"EXISTS": true, "FOR": true, "FROM": true, "FULL": true, "GROUP": true,
	"HAVING": true, "ILIKE": true, "IN": true, "INNER": true,
	"INSERT": true, "INTERSECT": true, "INTO": true, "IS": true,
	"JOIN": true, "LEFT": true, "LIKE": true, "LIMIT": true, "NATURAL": true,
	"NOT": true, "NULL": true, "OFFSET": true, "ON": true, "OR": true,
	"ORDER": true, "OUTER": true, "RETURNING": true, "RIGHT": true,
	"SELECT": true, "SET": true, "SIMILAR": true, "SOME": true,
	"STRAIGHT_JOIN": true, "THEN": true, "TO": true, "UNION": true,
	"UPDATE": true, "USING": true, "VALUES": true, "WHEN": true,
	"WHERE": true, "WITH": true,
}

type tokenKind uint8

const (
	tokIdent tokenKind = iota
	tokDot
	tokComma
	tokLparen
	tokRparen
	tokString
	tokSemicolon
	tokOther
)

type token struct {
	text string
	kind tokenKind
}

// tokenize splits line[:end] into tokens, skipping whitespace and comments,
// and folding quoted identifiers into tokIdent. String literals become a
// single tokString token so their contents never affect the scan.
func tokenize(line []rune, end int) []token {
	tokens := make([]token, 0, 16)
	for i := 0; i < end; {
		r := line[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '-' && i+1 < end && line[i+1] == '-':
			for i < end && line[i] != '\n' {
				i++
			}
		case r == '/' && i+1 < end && line[i+1] == '*':
			i += 2
			for i < end {
				if line[i] == '*' && i+1 < end && line[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
		case r == '\'':
			i++
			for i < end {
				if line[i] == '\'' {
					if i+1 < end && line[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			tokens = append(tokens, token{"", tokString})
		case r == '"' || r == '`':
			q := r
			i++
			start := i
			for i < end && line[i] != q {
				i++
			}
			tokens = append(tokens, token{string(line[start:i]), tokIdent})
			if i < end {
				i++
			}
		case r == '[':
			i++
			start := i
			for i < end && line[i] != ']' {
				i++
			}
			tokens = append(tokens, token{string(line[start:i]), tokIdent})
			if i < end {
				i++
			}
		case isIdentStart(r):
			start := i
			for i < end && isIdentChar(line[i]) {
				i++
			}
			tokens = append(tokens, token{string(line[start:i]), tokIdent})
		default:
			var kind tokenKind
			switch r {
			case '.':
				kind = tokDot
			case ',':
				kind = tokComma
			case '(':
				kind = tokLparen
			case ')':
				kind = tokRparen
			case ';':
				kind = tokSemicolon
			default:
				kind = tokOther
			}
			tokens = append(tokens, token{string(r), kind})
			i++
		}
	}
	return tokens
}

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_' || r == '$'
}

func isIdentChar(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

// isWordChar reports whether r can be part of the word at the cursor. Dots
// are included so qualified names stay a single word.
func isWordChar(r rune) bool {
	return isIdentChar(r) || r == '.'
}
