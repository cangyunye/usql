package completer

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// candMockReader serves a small fixed catalog:
//
//	public.film         TABLE  (id, name)
//	public.actor        TABLE  (id, film_id)
//	public.film_view    VIEW   (id, title)
//	public.now          FUNCTION
//	public.actor_id_seq SEQUENCE
//	other.orders        TABLE
//
// OnlyVisible resolves to the "public" schema (the session default).
type candMockReader struct{}

var _ interface {
	metadata.TableReader
	metadata.ColumnReader
	metadata.FunctionReader
	metadata.SequenceReader
} = &candMockReader{}

var candTables = []metadata.Table{
	{Catalog: "", Schema: "public", Name: "film", Type: "TABLE"},
	{Catalog: "", Schema: "public", Name: "actor", Type: "TABLE"},
	{Catalog: "", Schema: "public", Name: "film_view", Type: "VIEW"},
	{Catalog: "", Schema: "other", Name: "orders", Type: "TABLE"},
}

var candColumns = []metadata.Column{
	{Schema: "public", Table: "film", Name: "id", OrdinalPosition: 1, DataType: "integer"},
	{Schema: "public", Table: "film", Name: "name", OrdinalPosition: 2, DataType: "text"},
	{Schema: "public", Table: "actor", Name: "id", OrdinalPosition: 1, DataType: "integer"},
	{Schema: "public", Table: "actor", Name: "film_id", OrdinalPosition: 2, DataType: "integer"},
	{Schema: "public", Table: "film_view", Name: "id", OrdinalPosition: 1, DataType: "integer"},
	{Schema: "public", Table: "film_view", Name: "title", OrdinalPosition: 2, DataType: "text"},
}

var candFunctions = []metadata.Function{
	{Schema: "public", Name: "now", ArgTypes: "x integer", ResultType: "timestamp"},
}

var candSequences = []metadata.Sequence{
	{Schema: "public", Name: "actor_id_seq"},
}

// matchNamePattern interprets a metadata.Filter name pattern: a trailing %
// makes it a case-insensitive prefix match, as the readers do with LIKE.
func matchNamePattern(name, pattern string) bool {
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(strings.TrimSuffix(pattern, "%")))
}

func matchSchema(filterSchema, schema string) bool {
	return filterSchema == "" || strings.EqualFold(filterSchema, schema)
}

func (r candMockReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	var results []metadata.Table
	for _, t := range candTables {
		if !matchNamePattern(t.Name, f.Name) || !matchSchema(f.Schema, t.Schema) {
			continue
		}
		if len(f.Types) > 0 && !containsString(f.Types, t.Type) {
			continue
		}
		results = append(results, t)
	}
	return metadata.NewTableSet(results), nil
}

func (r candMockReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	var results []metadata.Column
	for _, col := range candColumns {
		if !strings.EqualFold(col.Table, f.Parent) {
			continue
		}
		if f.Schema != "" && !strings.EqualFold(col.Schema, f.Schema) {
			continue
		}
		results = append(results, col)
	}
	return metadata.NewColumnSet(results), nil
}

func (r candMockReader) Functions(f metadata.Filter) (*metadata.FunctionSet, error) {
	var results []metadata.Function
	for _, fn := range candFunctions {
		if !matchNamePattern(fn.Name, f.Name) || !matchSchema(f.Schema, fn.Schema) {
			continue
		}
		results = append(results, fn)
	}
	return metadata.NewFunctionSet(results), nil
}

func (r candMockReader) Sequences(f metadata.Filter) (*metadata.SequenceSet, error) {
	var results []metadata.Sequence
	for _, s := range candSequences {
		if !matchNamePattern(s.Name, f.Name) || !matchSchema(f.Schema, s.Schema) {
			continue
		}
		results = append(results, s)
	}
	return metadata.NewSequenceSet(results), nil
}

func (r candMockReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	if f.OnlyVisible {
		return metadata.NewSchemaSet([]metadata.Schema{{Schema: "public"}}), nil
	}
	return metadata.NewSchemaSet([]metadata.Schema{{Schema: "public"}, {Schema: "other"}}), nil
}

func containsString(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func discardLogger() logger {
	return log.New(io.Discard, "", 0)
}

// contextTestCompleter builds a completer with the lazy cache installed.
func contextTestCompleter(t *testing.T) *completer {
	t.Helper()
	c := &completer{reader: candMockReader{}, logger: discardLogger(), schemaKind: "schema"}
	WithContextCompletion()(c)
	if c.cache == nil {
		t.Fatal("WithContextCompletion did not install a cache")
	}
	return c
}

// settleCompleter waits until the L1 levels (all schemas, current schema)
// and the current schema's L2 objects have loaded. The get calls themselves
// arm the loads, so polling drives the lazy machinery.
func settleCompleter(t *testing.T, c *completer) {
	t.Helper()
	waitFor(t, func() bool {
		if _, ok := c.schemas(); !ok {
			return false
		}
		cur, ok := c.currentSchema()
		if !ok || cur == "" {
			return false
		}
		_, ok = c.schemaObjects("", cur)
		return ok
	})
}

// settleColumns waits until a table's L3 columns have loaded.
func settleColumns(t *testing.T, c *completer, ref TableRef) {
	t.Helper()
	waitFor(t, func() bool {
		objs, ok := c.columns(ref)
		return ok && len(objs) > 0
	})
}

// TestContextCompletion drives DoRepl — the replace-style path — with the
// lazy cache installed. Object positions offer the schema tier plus the
// current schema's objects; "schema." offers that namespace's objects;
// column positions offer the tables' columns; keywords ride along where the
// grammar allows them. Candidates are full words replacing the typed word.
func TestContextCompletion(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		start   int
		want    []string
		wantLen int
		wantNil bool
	}{
		{
			"from table prefix",
			"SELECT * FROM fi", 16,
			[]string{"public.film", "public.film_view"}, 2, false,
		},
		{
			"from empty word offers schemas then current schema objects",
			"SELECT * FROM ", 14,
			[]string{"other", "public", "public.now", "public.film", "public.actor", "public.film_view", "public.actor_id_seq"}, 0, false,
		},
		{
			"from namespace prefix ranks the namespace first",
			"SELECT * FROM pu", 16,
			[]string{"public", "public.now", "public.film", "public.actor", "public.film_view", "public.actor_id_seq"}, 2, false,
		},
		{
			"from namespace dot lists objects",
			"SELECT * FROM public.", 21,
			[]string{"public.now", "public.film", "public.actor", "public.film_view", "public.actor_id_seq"}, 7, false,
		},
		{
			"from bare table name falls back to qualified tables",
			"SELECT * FROM film", 18,
			[]string{"public.film", "public.film_view"}, 4, false,
		},
		{
			"insert into offers updatables only",
			"INSERT INTO fi", 14,
			[]string{"public.film", "public.film_view"}, 2, false,
		},
		{
			"update offers updatables only",
			"UPDATE fi", 9,
			[]string{"public.film", "public.film_view"}, 2, false,
		},
		{
			"insert into table done offers column list group",
			"INSERT INTO film ", 17,
			[]string{"(id, name)"}, 0, false,
		},
		{
			"insert into column group done offers values",
			"INSERT INTO film (id, name) v", 29,
			[]string{"values"}, 1, false,
		},
		{
			"values group hints all fields in written order",
			"INSERT INTO film (id, name, release_year) VALUES (", 50,
			[]string{"id", "name", "release_year"}, 0, false,
		},
		{
			"values group hints remaining fields after first value",
			"INSERT INTO film (id, name, release_year) VALUES (1, ", 53,
			[]string{"name", "release_year"}, 0, false,
		},
		{
			"values group skips string literal commas",
			"INSERT INTO film (id, name, release_year) VALUES (1, 'x, y', ", 61,
			[]string{"release_year"}, 0, false,
		},
		{
			"values group without column list uses table order",
			"INSERT INTO film VALUES (", 25,
			[]string{"id", "name"}, 0, false,
		},
		{
			"values group exhausted declines",
			"INSERT INTO film VALUES (1, 'a', ", 33,
			nil, 0, true,
		},
		{
			"insert into column list prefix",
			"INSERT INTO film (na", 20,
			[]string{"name"}, 2, false,
		},
		{
			"insert into column list",
			"INSERT INTO film (", 18,
			[]string{"id", "name"}, 0, false,
		},
		{
			"alias qualified column",
			"SELECT * FROM film f WHERE f.", 29,
			[]string{"f.id", "f.name"}, 2, false,
		},
		{
			"unqualified column after and",
			"SELECT * FROM film WHERE id = 1 AND na", 38,
			[]string{"name"}, 2, false,
		},
		{
			"join on column",
			"SELECT * FROM film JOIN actor ON fi", 35,
			[]string{"film_id"}, 2, false,
		},
		{
			"group by column",
			"SELECT * FROM film GROUP BY na", 30,
			[]string{"name"}, 2, false,
		},
		{
			"schema qualified table prefix",
			"SELECT * FROM public.fi", 23,
			[]string{"public.film", "public.film_view"}, 9, false,
		},
		{
			"join alias qualified column",
			"SELECT * FROM film f JOIN actor a ON f.", 39,
			[]string{"f.id", "f.name"}, 2, false,
		},
		{
			"unknown qualifier declines",
			"SELECT * FROM film f WHERE z.", 29,
			nil, 2, true,
		},
		{
			"table listed after semicolon",
			"SELECT 1; SELECT * FROM fi", 26,
			[]string{"public.film", "public.film_view"}, 2, false,
		},
		{
			"using column list",
			"SELECT * FROM film JOIN actor USING (fi", 39,
			[]string{"film_id"}, 2, false,
		},
		{
			"where offers columns and keywords",
			"SELECT * FROM film WHERE ", 25,
			[]string{
				"id", "OR", "IN", "AND", "NOT", "END", "name", "LIKE",
				"CASE", "WHEN", "THEN", "ELSE", "EXISTS", "IS NULL",
				"BETWEEN", "IS NOT NULL",
			}, 0, false,
		},
		{
			"insert verb offers INTO",
			"INSERT IN", 9,
			[]string{"INTO"}, 2, false,
		},
		{
			"delete verb offers FROM",
			"DELETE ", 7,
			[]string{"FROM"}, 0, false,
		},
		{
			"in a string literal declines",
			"SELECT * FROM film WHERE name = 'x", 34,
			nil, 2, true,
		},
		{
			"in a comment declines",
			"SELECT * FROM film -- lo", 24,
			nil, 2, true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			c := contextTestCompleter(t)
			c.DoRepl([]rune(test.line), test.start) // trigger the lazy loads
			settleCompleter(t, c)
			settleColumns(t, c, TableRef{Name: "film"})
			got, length, ok := c.DoRepl([]rune(test.line), test.start)
			if test.wantNil {
				if ok && len(got) > 0 {
					t.Fatalf("DoRepl(%q, %d) = %q, want nothing", test.line, test.start, got)
				}
				return
			}
			if length != test.wantLen {
				t.Errorf("length = %d, want %d", length, test.wantLen)
			}
			if len(got) != len(test.want) {
				t.Fatalf("DoRepl(%q, %d) = %q, want %q", test.line, test.start, got, test.want)
			}
			for i := range got {
				if got[i].Text != test.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i].Text, test.want[i])
				}
			}
		})
	}
}

// TestContextCompletionDeclinesToFallback: when the context engine declines
// (inconclusive position, table already listed), Do answers with keywords —
// never a query — and empty results decline entirely.
func TestContextCompletionDeclinesToFallback(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(candMockReader{}),
		WithLogger(discardLogger()),
		WithContextCompletion(),
	).(*completer)

	cases := []struct {
		name    string
		line    string
		start   int
		want    []string
		wantLen int
	}{
		{
			"empty line offers statement keywords",
			"", 0,
			CommonSqlStartCommands, 0,
		},
		{
			"table already listed offers clause keywords",
			"SELECT * FROM film ", 19,
			CommonSqlCommands, 0,
		},
		{
			"insert into without column metadata offers keywords",
			"INSERT INTO pg_catalog.pg_class ", 32,
			CommonSqlCommands, 0,
		},
		{
			"select statement keyword",
			"SELECT 1; sel", 13,
			[]string{"ect"}, 3,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, length := c.Do([]rune(test.line), test.start)
			if length != test.wantLen {
				t.Errorf("length = %d, want %d", length, test.wantLen)
			}
			if len(got) != len(test.want) {
				t.Fatalf("Do(%q, %d) = %q, want %q", test.line, test.start, got, test.want)
			}
			for i := range got {
				if got[i].Text != test.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i].Text, test.want[i])
				}
			}
		})
	}
}

// TestCompleteWithContextOrder checks the ranking, not the data source: both
// film and film_view match "fi" as prefix-of-last-segment, and the shorter
// one wins the tie.
func TestCompleteWithContextOrder(t *testing.T) {
	c := contextTestCompleter(t)
	c.DoRepl([]rune("SELECT * FROM fi"), 16) // arm the loads
	settleCompleter(t, c)
	got := c.completeWithContext([]string{"FROM", "*", "SELECT"}, []rune("fi"))
	if len(got) != 2 || got[0].Text != "public.film" || got[1].Text != "public.film_view" {
		t.Errorf("completeWithContext(fi) = %q, want [public.film public.film_view]", got)
	}
}

// TestCompletionQueryCounts pins the lazy cache's promise: each level loads
// exactly once per scope, no matter how many keystrokes re-request it. The
// polls re-issue the request, mirroring the UI's kick-driven re-render.
func TestCompletionQueryCounts(t *testing.T) {
	inner := newCountingReader()
	c := &completer{reader: inner, logger: discardLogger(), schemaKind: "schema"}
	WithContextCompletion()(c)

	// the FROM position: one schemas load (all + current = 2 queries) and
	// one public-objects load, however many keystrokes re-request it
	waitFor(t, func() bool {
		c.DoRepl([]rune("SELECT * FROM "), 14)
		return inner.calls("tables") >= 1
	})
	if n := inner.calls("schemas"); n != 2 {
		t.Fatalf("schemas queried %d times, want 2 (all + current)", n)
	}
	if n := inner.calls("tables"); n != 1 {
		t.Fatalf("tables queried %d times, want 1", n)
	}

	// a second namespace: exactly one more tables query
	waitFor(t, func() bool {
		c.DoRepl([]rune("SELECT * FROM other."), 20)
		return inner.calls("tables") >= 2
	})
	if n := inner.calls("tables"); n != 2 {
		t.Fatalf("tables queried %d times after other., want 2", n)
	}

	// columns: one query for film, however many times its columns complete
	waitFor(t, func() bool {
		c.DoRepl([]rune("SELECT * FROM film f WHERE f."), 29)
		return inner.calls("columns") >= 1
	})
	if n := inner.calls("columns"); n != 1 {
		t.Fatalf("columns queried %d times, want 1", n)
	}

	// a scope change drops everything: the next request re-queries
	c.Invalidate()
	waitFor(t, func() bool {
		c.DoRepl([]rune("SELECT * FROM film f WHERE f."), 29)
		return inner.calls("columns") >= 2
	})
	if n := inner.calls("columns"); n != 2 {
		t.Fatalf("columns queried %d times after invalidate, want 2", n)
	}
}

// TestReconstructLine checks the line rebuild from Do's previousWords. A
// trailing empty text leaves a trailing separator, which is fine — the
// token stream, the only thing the clause scanner inspects, is unaffected.
func TestReconstructLine(t *testing.T) {
	cases := []struct {
		name          string
		previousWords []string
		text          string
		want          string
	}{
		{
			"plain words",
			[]string{"WHERE", "film", "FROM", "*", "SELECT"}, "na",
			"SELECT * FROM film WHERE na",
		},
		{
			"parenthesized word",
			[]string{"(", "film", "INTO", "INSERT"}, "",
			"INSERT INTO film ( ",
		},
		{
			"no previous words",
			nil, "SEL",
			"SEL",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := string(reconstructLine(test.previousWords, []rune(test.text)))
			if got != test.want {
				t.Errorf("reconstructLine = %q, want %q", got, test.want)
			}
		})
	}
}

// TestFunctionCompletion checks the function tier in expression positions:
// catalog functions (bare names, catalog signature) and static built-ins
// insert "name(" and carry the signature as dim detail.
func TestFunctionCompletion(t *testing.T) {
	c := contextTestCompleter(t)
	line := "SELECT * FROM film WHERE no"
	c.DoRepl([]rune(line), len(line)) // arm the lazy loads
	settleCompleter(t, c)
	settleColumns(t, c, TableRef{Name: "film"})
	got, length, ok := c.DoRepl([]rune(line), len(line))
	if !ok || length != 2 {
		t.Fatalf("DoRepl(%q) = ok %v, length %d; want ok, 2", line, ok, length)
	}
	// the NOT keyword matches the prefix too, and ranks first by length
	if len(got) != 2 || got[0].Text != "not" || got[1].Text != "now(" ||
		got[1].Kind != "function" || got[1].Detail != "x integer) → timestamp" {
		t.Fatalf("DoRepl(%q) = %+v, want [not now( function \"x integer) → timestamp\"]", line, got)
	}

	// built-ins: a one-letter prefix keeps the menu small, ranked
	// shortest-first, all lower-cased after the lower-case pattern
	line = "SELECT * FROM film WHERE s"
	got, _, _ = c.DoRepl([]rune(line), len(line))
	want := []string{"sum(", "substring(", "session_user"}
	if len(got) != len(want) {
		t.Fatalf("DoRepl(%q) = %q, want %q", line, candTexts(got), want)
	}
	for i, w := range want {
		if got[i].Text != w || got[i].Kind != "function" {
			t.Errorf("got[%d] = %q (%q), want %q (function)", i, got[i].Text, got[i].Kind, w)
		}
	}
	if got[0].Detail != "expr)" {
		t.Errorf("sum detail = %q, want %q", got[0].Detail, "expr)")
	}
}

// TestFunctionCompletionPlacement pins where functions do NOT appear: the
// INSERT INTO column list, a call's argument list, and empty-word positions.
func TestFunctionCompletionPlacement(t *testing.T) {
	c := contextTestCompleter(t)
	arm := func(line string) {
		c.DoRepl([]rune(line), len(line))
		settleCompleter(t, c)
		settleColumns(t, c, TableRef{Name: "film"})
	}

	// the INSERT INTO column list takes plain columns only — matching
	// nothing here declines outright
	line := "INSERT INTO film (co"
	arm(line)
	if got, _, ok := c.DoRepl([]rune(line), len(line)); ok && len(got) > 0 {
		t.Fatalf("DoRepl(%q) = %q, want nothing", line, candTexts(got))
	}

	// inside a call's argument list (ctx.CallName) functions stand down,
	// columns still complete
	line = "SELECT * FROM film WHERE lower(x, na"
	arm(line)
	got, _, ok := c.DoRepl([]rune(line), len(line))
	if !ok || len(got) != 1 || got[0].Text != "name" {
		t.Fatalf("DoRepl(%q) = %q (ok %v), want [name]", line, candTexts(got), ok)
	}

	// an empty word keeps the menu to columns and clause keywords
	line = "SELECT * FROM film WHERE "
	arm(line)
	got, _, _ = c.DoRepl([]rune(line), len(line))
	for _, cand := range got {
		if cand.Kind == "function" {
			t.Fatalf("DoRepl(%q) offered function %q at an empty word", line, cand.Text)
		}
	}
}

// TestValuesFieldHintsTypes checks the VALUES position's ordered hints: each
// shows the column with its data type as dim detail, taken from the written
// column list or the table's metadata order — and a nested call's ')' no
// longer ends the values group, so hints survive now() values.
func TestValuesFieldHintsTypes(t *testing.T) {
	c := contextTestCompleter(t)
	arm := func(line string) {
		c.DoRepl([]rune(line), len(line))
		settleCompleter(t, c)
		settleColumns(t, c, TableRef{Name: "film"})
	}
	type hint struct{ text, detail string }
	check := func(line string, want []hint) {
		t.Helper()
		got, _, ok := c.DoRepl([]rune(line), len(line))
		if !ok {
			t.Fatalf("DoRepl(%q) declined", line)
		}
		if len(got) != len(want) {
			t.Fatalf("DoRepl(%q) = %+v, want %d hints", line, got, len(want))
		}
		for i, w := range want {
			if got[i].Text != w.text || got[i].Detail != w.detail || got[i].Kind != "column" {
				t.Errorf("got[%d] = %q/%q (%q), want %q/%q (column)",
					i, got[i].Text, got[i].Detail, got[i].Kind, w.text, w.detail)
			}
		}
	}

	arm("INSERT INTO film (id, name) VALUES (")
	check("INSERT INTO film (id, name) VALUES (",
		[]hint{{"id", ":integer"}, {"name", ":text"}})

	// without a written list, the table's metadata order
	arm("INSERT INTO film VALUES (")
	check("INSERT INTO film VALUES (",
		[]hint{{"id", ":integer"}, {"name", ":text"}})

	// a now() value no longer closes the values group
	line := "INSERT INTO film (id, name) VALUES (now(), "
	arm(line)
	check(line, []hint{{"name", ":text"}})

	// typing a value offers the matching function calls after the hints
	line = "INSERT INTO film (id, name) VALUES (100, no"
	arm(line)
	got, _, ok := c.DoRepl([]rune(line), len(line))
	if !ok || len(got) != 2 || got[0].Text != "name" || got[0].Detail != ":text" ||
		got[1].Text != "now(" || got[1].Kind != "function" {
		t.Fatalf("DoRepl(%q) = %+v (ok %v), want [name/:text now(]", line, got, ok)
	}
}

// candTexts extracts candidates' text.
func candTexts(cands []rline.Cand) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Text)
	}
	return out
}
