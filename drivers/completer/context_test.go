package completer

import (
	"reflect"
	"testing"

	"github.com/xo/usql/drivers/metadata"
)

func TestParseContext(t *testing.T) {
	film := TableRef{Name: "film"}
	pf := TableRef{Schema: "public", Name: "film"}
	actor := TableRef{Name: "actor"}

	cases := []struct {
		name          string
		line          string
		start         int
		expClause     string
		expQualifier  string
		expObject     string
		expAfterParen bool
		expTables     []TableRef
		expAliases    map[string]TableRef
	}{
		{
			"empty line",
			"", 0,
			"", "", "", false,
			nil, map[string]TableRef{},
		},
		{
			"bare keyword",
			"SEL", 3,
			"", "", "SEL", false,
			nil, map[string]TableRef{},
		},
		{
			"where clause",
			"SELECT * FROM film WHERE ", 25,
			"WHERE", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"where clause, lower case",
			"select * from film where ", 25,
			"WHERE", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"alias qualified word",
			"SELECT f.name FROM film f WHERE f.", 34,
			"WHERE", "f.", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film, "f": film},
		},
		{
			"alias as keyword",
			"SELECT * FROM film AS f WHERE f.", 32,
			"WHERE", "f.", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film, "f": film},
		},
		{
			"schema qualified word",
			"SELECT * FROM public.", 21,
			"FROM", "public.", "", false,
			nil, map[string]TableRef{},
		},
		{
			"catalog.schema qualified word",
			"SELECT * FROM remote.default.f", 30,
			"FROM", "remote.default.", "f", false,
			nil, map[string]TableRef{},
		},
		{
			"qualified schema table alias",
			"select * from public.film pf where pf.", 38,
			"WHERE", "pf.", "", false,
			[]TableRef{pf}, map[string]TableRef{"film": pf, "pf": pf},
		},
		{
			"comma separated tables",
			"select * from film f, actor a where ", 36,
			"WHERE", "", "", false,
			[]TableRef{film, actor}, map[string]TableRef{"film": film, "f": film, "actor": actor, "a": actor},
		},
		{
			"update set clause",
			"UPDATE film SET name = ", 23,
			"SET", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"insert into after paren",
			"INSERT INTO film (", 18,
			"INTO", "", "", true,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"insert into column list",
			"INSERT INTO film (a", 19,
			"INTO", "", "a", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"insert values",
			"INSERT INTO film (a, b) VALUES (", 32,
			"VALUES", "", "", true,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"group by clause",
			"SELECT * FROM film GROUP BY ", 28,
			"GROUP", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"join on clause",
			"SELECT * FROM film JOIN actor ON ", 34,
			"ON", "", "", false,
			[]TableRef{film, actor}, map[string]TableRef{"film": film, "actor": actor},
		},
		{
			"order by clause",
			"SELECT name FROM film ORDER BY ", 31,
			"ORDER", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"having clause",
			"SELECT * FROM film GROUP BY id HAVING ", 38,
			"HAVING", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"join with aliases",
			"SELECT * FROM film f JOIN actor a ON f.id = a.film_id", 53,
			"ON", "a.", "film_id", false,
			[]TableRef{film, actor}, map[string]TableRef{"film": film, "f": film, "actor": actor, "a": actor},
		},
		{
			"line comment skipped",
			"SELECT 1 -- note\nFROM film WHERE ", 33,
			"WHERE", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"complete subquery group skipped",
			"SELECT * FROM (SELECT id FROM film) s WHERE ", 44,
			"WHERE", "", "", false,
			nil, map[string]TableRef{"s": {}},
		},
		{
			"incomplete subquery parsed",
			"SELECT * FROM (SELECT id FROM film WHERE ", 41,
			"WHERE", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"cte body parsed at cursor",
			"WITH x AS (SELECT id FROM film WHERE ", 37,
			"WHERE", "", "", false,
			[]TableRef{film}, map[string]TableRef{"film": film},
		},
		{
			"select list before from",
			"SELECT f. FROM film f", 9,
			"SELECT", "f.", "", false,
			nil, map[string]TableRef{},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := parseContext([]rune(test.line), test.start)
			if ctx.Clause != test.expClause {
				t.Errorf("Clause = %q, want %q", ctx.Clause, test.expClause)
			}
			if ctx.Qualifier != test.expQualifier {
				t.Errorf("Qualifier = %q, want %q", ctx.Qualifier, test.expQualifier)
			}
			if ctx.Object != test.expObject {
				t.Errorf("Object = %q, want %q", ctx.Object, test.expObject)
			}
			if ctx.AfterParen != test.expAfterParen {
				t.Errorf("AfterParen = %v, want %v", ctx.AfterParen, test.expAfterParen)
			}
			if !reflect.DeepEqual(ctx.Tables, test.expTables) {
				t.Errorf("Tables = %v, want %v", ctx.Tables, test.expTables)
			}
			if !reflect.DeepEqual(ctx.Aliases, test.expAliases) {
				t.Errorf("Aliases = %v, want %v", ctx.Aliases, test.expAliases)
			}
		})
	}
}

// TestParseContextParensAndTableListed covers the fields the candidate
// generators branch on: OpenParens distinguishes INSERT INTO column lists,
// and TableListed distinguishes INSERT INTO film <cursor> (decline) from
// FROM film JOIN <cursor> (still offer tables).
func TestParseContextParensAndTableListed(t *testing.T) {
	cases := []struct {
		name           string
		line           string
		start          int
		expOpenParens  int
		expTableListed bool
	}{
		{"insert into paren", "INSERT INTO film (", 18, 1, true},
		{"insert into column list", "INSERT INTO film (a", 19, 1, true},
		{"insert into values paren", "INSERT INTO film (a, b) VALUES (", 32, 1, false},
		{"from table listed", "SELECT * FROM film ", 19, 0, true},
		{"from before table", "SELECT * FROM ", 14, 0, false},
		{"join after from table", "SELECT * FROM film JOIN ", 24, 0, false},
		{"closed parens", "SELECT count(*) FROM film WHERE ", 31, 0, true},
		{"unclosed subquery", "SELECT * FROM (SELECT id FROM film WHERE ", 41, 1, false},
		{"after semicolon", "SELECT 1; SELECT * FROM fi", 26, 0, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := parseContext([]rune(test.line), test.start)
			if ctx.OpenParens != test.expOpenParens {
				t.Errorf("OpenParens = %d, want %d", ctx.OpenParens, test.expOpenParens)
			}
			if ctx.TableListed != test.expTableListed {
				t.Errorf("TableListed = %v, want %v", ctx.TableListed, test.expTableListed)
			}
		})
	}
}

// ctxMockReader is intentionally separate from mockReader in completer_test.go
// so these tests do not depend on it while the main code is in flux.
type ctxMockReader struct{}

var _ metadata.ColumnReader = &ctxMockReader{}

func (r ctxMockReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	if f.Parent != "film" {
		return metadata.NewColumnSet(nil), nil
	}
	return metadata.NewColumnSet([]metadata.Column{
		{Name: "id"},
		{Name: "name"},
	}), nil
}

// TestContextCandidates exercises the plan B pipeline end-to-end:
// parseContext -> alias/table resolution -> reader query -> fuzzy completion.
func TestContextCandidates(t *testing.T) {
	c := completer{reader: ctxMockReader{}, logger: discardLogger()}

	cases := []struct {
		name  string
		line  string
		start int
		want  []string
	}{
		{
			"unqualified column in where",
			"SELECT * FROM film WHERE na", 27,
			[]string{"me"},
		},
		{
			"alias qualified column",
			"SELECT * FROM film f WHERE f.", 29,
			[]string{"id", "name"},
		},
		{
			"unknown alias yields nothing",
			"SELECT * FROM film f WHERE x.", 29,
			nil,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := c.completeFromContext(parseContext([]rune(test.line), test.start))
			if len(got) != len(test.want) {
				t.Fatalf("completeFromContext(%q, %d) = %q, want %q", test.line, test.start, got, test.want)
			}
			for i := range got {
				if string(got[i]) != test.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, string(got[i]), test.want[i])
				}
			}
		})
	}
}
