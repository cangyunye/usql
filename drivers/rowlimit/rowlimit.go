// Package rowlimit caps interactive, unfiltered SELECT queries at a row
// limit, in the connected database's own syntax.
package rowlimit

import (
	"strconv"
	"strings"
)

// row limit clause strategies, matched per driver:
//
//	limit      append "LIMIT n" — PostgreSQL, MySQL, SQLite, openGauss,
//	           OceanBase (MySQL mode), ClickHouse, and most others
//	fetchfirst append "FETCH FIRST n ROWS ONLY" — Oracle 12c+ and OceanBase
//	           Oracle mode (OB recommends the row limiting clause over
//	           ROWNUM subqueries)
//	top        insert "TOP (n)" after SELECT [DISTINCT|ALL] — SQL Server,
//	           SAP ASE
//	none       drivers whose row limit syntax is neither safe to guess nor
//	           appendable — never rewritten
//
// Syntax verified against vendor documentation (2026-09): OceanBase
// row-limiting clause recommendation, openGauss SELECT reference, Oracle
// 12c FETCH FIRST (11g only has ROWNUM), and the SQL Server TOP clause
// position (after SELECT DISTINCT/ALL).
var rowLimitStrategies = map[string]string{
	// Oracle family
	"oracle":   "fetchfirst",
	"godror":   "fetchfirst",
	"oboracle": "fetchfirst",
	// TOP family
	"sqlserver": "top",
	"sapase":    "top",
	// unguessable or non-appendable syntax
	"firebird": "none", // SELECT FIRST n (pre-3.0); FETCH FIRST on 3.0+
	"adodb":    "none", // OLE DB: access (TOP) or others
	"odbc":     "none", // any backend
}

// Apply rewrites an unfiltered SELECT query to return at most n rows, using
// the named driver's row limit syntax. It returns the rewritten statement
// and whether anything changed; statements that already carry a row
// restriction, filter, or set operation are returned unchanged, as are
// statements whose driver syntax is unknown. The rewrite is meant for
// interactive ad-hoc queries (see the ROWLIMIT variable).
func Apply(driver string, sqlstr string, n int) (string, bool) {
	if n <= 0 {
		return sqlstr, false
	}
	strategy, ok := rowLimitStrategies[driver]
	if !ok {
		strategy = "limit"
	}
	if strategy == "none" {
		return sqlstr, false
	}
	words := scanWords(sqlstr)
	if len(words) == 0 {
		return sqlstr, false
	}
	switch words[0].upper {
	case "SELECT", "WITH", "TABLE":
	default:
		return sqlstr, false
	}
	for _, w := range words {
		switch w.upper {
		case "WHERE", "LIMIT", "OFFSET", "FETCH", "TOP", "ROWNUM", "FIRST",
			"UNION", "INTERSECT", "EXCEPT", "MINUS":
			// filtered, already limited, or a set operation: the clause
			// would only apply to the last query block — leave it alone
			return sqlstr, false
		case "FOR":
			// FOR UPDATE/SHARE: the limit clause must precede the lock
			// clause, which a trailing append would violate
			return sqlstr, false
		}
	}
	// a leading newline keeps the clause out of a trailing line comment
	clause := "\nLIMIT " + strconv.Itoa(n)
	switch strategy {
	case "fetchfirst":
		clause = "\nFETCH FIRST " + strconv.Itoa(n) + " ROWS ONLY"
	case "top":
		// TOP is part of the SELECT head: not appendable, and only valid
		// when the statement starts with SELECT (not WITH/TABLE)
		if words[0].upper != "SELECT" {
			return sqlstr, false
		}
		at := words[0].end
		// TOP follows DISTINCT/ALL: SELECT DISTINCT TOP (n) ...
		if len(words) > 1 && (words[1].upper == "DISTINCT" || words[1].upper == "ALL") {
			at = words[1].end
		}
		return sqlstr[:at] + " TOP (" + strconv.Itoa(n) + ")" + sqlstr[at:], true
	}
	return strings.TrimRight(sqlstr, " \t\r\n;") + clause, true
}

// word is a bare (unquoted, uncommented) word in a statement, with the byte
// span it came from.
type word struct {
	upper      string
	start, end int
}

// scanWords returns the bare words of sqlstr, upper-cased. Quoted strings,
// quoted identifiers, and comments never yield words, so a literal 'where'
// or a column named "top" cannot trip the filters.
func scanWords(s string) []word {
	var words []word
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if b.Len() > 0 {
			words = append(words, word{strings.ToUpper(b.String()), start, end})
			b.Reset()
			start = -1
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			flush(i)
			i = skipQuoted(s, i)
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			flush(i)
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			flush(i)
			i += 2
			for i < len(s) {
				if s[i] == '*' && i+1 < len(s) && s[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
		case c == '[':
			flush(i)
			i++
			for i < len(s) && s[i] != ']' {
				i++
			}
			i++
		case c == '_' || c == '$' || c == '#' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			if start < 0 {
				start = i
			}
			b.WriteByte(c)
			i++
		default:
			flush(i)
			i++
		}
	}
	flush(len(s))
	return words
}

// skipQuoted returns the index just past the quoted run starting at s[i]
// ('...', "...", `...`), honoring doubled quote escapes.
func skipQuoted(s string, i int) int {
	q := s[i]
	i++
	for i < len(s) {
		if s[i] == q {
			if i+1 < len(s) && s[i+1] == q {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}
