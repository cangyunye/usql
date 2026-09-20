package completer

import (
	"strings"
	"testing"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// TestCompleter pins the append-style Do path: statement and clause
// keywords, backslash commands, meta-command arguments, variables and files.
// Object completion is replace-style (DoRepl) and is covered by the context
// tests; Do never queries the database for objects.
func TestCompleter(t *testing.T) {
	cases := []struct {
		name           string
		line           string
		start          int
		expSuggestions []string
		expLength      int
	}{
		{
			"Single SQL keyword, uppercase",
			"SEL",
			3,
			[]string{
				"ECT",
			},
			3,
		},
		{
			"Single SQL keyword, lowercase",
			"ex",
			2,
			[]string{
				"ec",
				"ecute",
				"plain",
			},
			2,
		},
		{
			"usql command",
			`\dt`,
			3,
			[]string{
				`+`,
				``,
				`S+`,
				`S`,
			},
			3,
		},
		{
			"files",
			`\i comp`,
			7,
			[]string{
				"completer.go",
				"completer_test.go",
			},
			4,
		},
		{
			"connections",
			`\c p`,
			4,
			[]string{
				"pg://",
			},
			1,
		},
		{
			"mid-statement keywords",
			"SELECT * F",
			10,
			[]string{
				"ULL OUTER JOIN",
				"ROM",
				"ETCH",
			},
			1,
		},
		{
			"mid-statement empty word offers the clause keywords",
			"SELECT * FROM ",
			14,
			CommonSqlCommands,
			0,
		},
		{
			"insert",
			"INS",
			3,
			[]string{
				"ERT",
			},
			3,
		},
		{
			"insert keyword continuation falls to clause keywords",
			"INSERT IN",
			9,
			[]string{
				"",
				"NER JOIN",
			},
			2,
		},
		{
			"insert into table select",
			"INSERT INTO film SE",
			19,
			[]string{
				"LECT",
			},
			2,
		},
		{
			"variables",
			":a",
			2,
			[]string{},
			2,
		},
		{
			"terminated statement completes nothing",
			"SELECT 1;",
			9,
			nil,
			0,
		},
		{
			"terminated statement with trailing space completes nothing",
			"SELECT 1; ",
			10,
			nil,
			0,
		},
		{
			"string semicolon then real terminator completes nothing",
			"SELECT ';' WHERE id = 1; ",
			25,
			nil,
			0,
		},
		{
			"new word after terminator completes normally",
			"SELECT 1; sel",
			13,
			[]string{
				"ect",
			},
			3,
		},
	}

	completer := NewDefaultCompleter(WithReader(mockReader{}), WithConnStrings([]string{"pg://"}))
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			suggestions, length := completer.Do([]rune(test.line), test.start)
			// need at least 2 pairs of nested loops, one for what's missing, second for what's extra
			for _, exp := range test.expSuggestions {
				found := false
				for _, act := range suggestions {
					if act.Text == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Missing expected suggestion: %s", exp)
				}
			}
			for _, act := range suggestions {
				found := false
				for _, exp := range test.expSuggestions {
					if act.Text == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Unexpected suggestion: %s", act.Text)
				}
			}
			if length != test.expLength {
				t.Errorf("Expected Do() to return length %d, got %d", test.expLength, length)
			}
		})
	}
}

// TestDriverHookCompletion: a driver's beforeComplete hook (MySQL's USE
// databases) answers positions the context engine declines.
func TestDriverHookCompletion(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(mockReader{}),
		WithBeforeComplete(func(previousWords []string, text []rune) []rline.Cand {
			if len(previousWords) > 0 && strings.EqualFold(previousWords[len(previousWords)-1], "USE") {
				return completeFromListKind("database", IGNORE_CASE, text, "mysql", "other")
			}
			return nil
		}),
	).(*completer)

	got, length := c.Do([]rune("USE my"), 6)
	if length != 2 {
		t.Fatalf("length = %d, want 2", length)
	}
	if len(got) != 1 || got[0].Text != "sql" {
		t.Fatalf("USE my = %q, want [sql]", got)
	}
	// the context engine must not shadow the hook: DoRepl declines USE
	if _, _, ok := c.DoRepl([]rune("USE my"), 6); ok {
		t.Fatal("DoRepl should decline a USE position")
	}
}

type mockReader struct{}

var _ metadata.CatalogReader = &mockReader{}
var _ metadata.BasicReader = &mockReader{}

func (r mockReader) Catalogs(metadata.Filter) (*metadata.CatalogSet, error) {
	return metadata.NewCatalogSet([]metadata.Catalog{
		{
			Catalog: "main",
		},
		{
			Catalog: "remote",
		},
	}), nil
}

func (r mockReader) Schemas(metadata.Filter) (*metadata.SchemaSet, error) {
	return metadata.NewSchemaSet([]metadata.Schema{
		{
			Schema:  "default",
			Catalog: "main",
		},
		{
			Schema:  "system",
			Catalog: "main",
		},
	}), nil
}

func (r mockReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	return metadata.NewTableSet([]metadata.Table{
		{
			Catalog: f.Catalog,
			Schema:  f.Schema,
			Name:    "film",
		},
		{
			Catalog: f.Catalog,
			Schema:  f.Schema,
			Name:    "factory",
		},
	}), nil
}

func (r mockReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	if f.Parent == "film" {
		return metadata.NewColumnSet([]metadata.Column{
			{
				Name: "id",
			},
			{
				Name: "name",
			},
		}), nil
	}
	return metadata.NewColumnSet([]metadata.Column{
		{
			Name: f.Catalog,
		},
		{
			Name: f.Schema,
		},
		{
			Name: f.Name,
		},
	}), nil
}
