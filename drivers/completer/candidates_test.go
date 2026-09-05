package completer

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/xo/usql/drivers/metadata"
)

// candMockReader serves a small fixed schema:
//
//	public.film         TABLE  (id, name)
//	public.actor        TABLE  (id, film_id)
//	public.film_view    VIEW   (id, title)
//	public.now          FUNCTION
//	public.actor_id_seq SEQUENCE
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
}

var candColumns = map[string][]string{
	"film":      {"id", "name"},
	"actor":     {"id", "film_id"},
	"film_view": {"id", "title"},
}

var candFunctions = []metadata.Function{
	{Schema: "public", Name: "now"},
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
	for _, name := range candColumns[strings.ToLower(f.Parent)] {
		results = append(results, metadata.Column{Table: f.Parent, Name: name})
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

// TestWithContextCompletion drives readline's Do with the
// WithContextCompletion option installed. The completer carries no static
// command lists, so whenever the context path declines, the heuristics below
// it can only answer from the mock reader — making it obvious which path
// produced the result.
func TestWithContextCompletion(t *testing.T) {
	c := completer{reader: candMockReader{}, logger: discardLogger()}
	WithContextCompletion()(&c)

	cases := []struct {
		name    string
		line    string
		start   int
		want    []string
		wantLen int
	}{
		{
			"from table prefix",
			"SELECT * FROM fi", 16,
			[]string{"lm", "lm_view"}, 2,
		},
		{
			"from empty word offers tables, functions, sequences",
			"SELECT * FROM ", 14,
			[]string{"now", "film", "actor", "film_view", "actor_id_seq"}, 0,
		},
		{
			"insert into offers updatables only",
			"INSERT INTO fi", 14,
			[]string{"lm", "lm_view"}, 2,
		},
		{
			"update offers updatables only",
			"UPDATE fi", 9,
			[]string{"lm", "lm_view"}, 2,
		},
		{
			"insert into column list prefix",
			"INSERT INTO film (na", 20,
			[]string{"me"}, 2,
		},
		{
			"insert into column list",
			"INSERT INTO film (", 18,
			[]string{"id", "name"}, 0,
		},
		{
			"alias qualified column",
			"SELECT * FROM film f WHERE f.", 29,
			[]string{"id", "name"}, 2,
		},
		{
			"unqualified column after and",
			"SELECT * FROM film WHERE id = 1 AND na", 38,
			[]string{"me"}, 2,
		},
		{
			"join on column",
			"SELECT * FROM film JOIN actor ON fi", 35,
			[]string{"lm_id"}, 2,
		},
		{
			"group by column",
			"SELECT * FROM film GROUP BY na", 30,
			[]string{"me"}, 2,
		},
		{
			"schema qualified table",
			"SELECT * FROM public.", 21,
			[]string{"now", "film", "actor", "film_view", "actor_id_seq"}, 7,
		},
		{
			"schema qualified table prefix",
			"SELECT * FROM public.fi", 23,
			[]string{"lm", "lm_view"}, 9,
		},
		{
			"join alias qualified column",
			"SELECT * FROM film f JOIN actor a ON f.", 39,
			[]string{"id", "name"}, 2,
		},
		{
			"unknown qualifier declines",
			"SELECT * FROM film f WHERE z.", 29,
			nil, 2,
		},
		{
			"table listed after semicolon",
			"SELECT 1; SELECT * FROM fi", 26,
			[]string{"lm", "lm_view"}, 2,
		},
		{
			"using column list",
			"SELECT * FROM film JOIN actor USING (fi", 39,
			[]string{"lm_id"}, 2,
		},
		{
			"where offers columns and keywords",
			"SELECT * FROM film WHERE ", 25,
			[]string{
				"IN", "OR", "id", "AND", "END", "NOT", "CASE", "ELSE",
				"LIKE", "THEN", "WHEN", "name", "EXISTS", "BETWEEN",
				"IS NULL", "IS NOT NULL",
			}, 0,
		},
		{
			"backslash commands still complete via heuristics",
			`\dt `, 4,
			[]string{"actor", "film"}, 0,
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
				if string(got[i]) != test.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, string(got[i]), test.want[i])
				}
			}
		})
	}
}

// TestWithContextCompletionFallsThrough uses a fully populated completer and
// checks that when the context path declines, the tail-matching heuristics
// answer as before.
func TestWithContextCompletionFallsThrough(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(candMockReader{}),
		WithLogger(discardLogger()),
		WithContextCompletion(),
	).(completer)

	cases := []struct {
		name    string
		line    string
		start   int
		want    []string
		wantLen int
	}{
		{
			"insert into table listed",
			"INSERT INTO film ", 17,
			[]string{"(", "DEFAULT VALUES", "SELECT", "TABLE", "VALUES", "OVERRIDING"}, 0,
		},
		{
			"table already listed",
			"SELECT * FROM film ", 19,
			CommonSqlCommands, 0,
		},
		{
			"empty line",
			"", 0,
			CommonSqlStartCommands, 0,
		},
		{
			"context result replaces whole dotted word",
			"SELECT * FROM public.fi", 23,
			[]string{"lm", "lm_view"}, 9,
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
				if string(got[i]) != test.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, string(got[i]), test.want[i])
				}
			}
		})
	}
}

// TestCompleteWithContextOrder checks that fuzzy ranking, not alphabetical
// order, decides the result: both film and film_view match "fi" with the
// same score, and the shorter one wins the tie.
func TestCompleteWithContextOrder(t *testing.T) {
	c := completer{reader: candMockReader{}, logger: discardLogger()}
	got := c.completeWithContext([]string{"FROM", "*", "SELECT"}, []rune("fi"))
	if len(got) != 2 || string(got[0]) != "lm" || string(got[1]) != "lm_view" {
		t.Errorf("completeWithContext(fi) = %q, want [lm lm_view]", got)
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
