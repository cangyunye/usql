package completer

import (
	"testing"
)

// TestStatementWords pins the "PRAGMA <name>" / "ATTACH <DATABASE>" rule:
// the registered words are served immediately after the verb, and never
// later into the statement — where the generic keywords apply again.
func TestStatementWords(t *testing.T) {
	c := NewDefaultCompleter(WithStatementWords(map[string][]string{
		"PRAGMA": {"journal_mode", "foreign_keys"},
		"ATTACH": {"DATABASE"},
	})).(*completer)

	cases := []struct {
		line string
		want []string
	}{
		{"PRAGMA ", []string{"journal_mode", "foreign_keys"}},
		{"PRAGMA fore", []string{"foreign_keys"}},
		{"pragma fore", []string{"foreign_keys"}},
		{"ATTACH ", []string{"DATABASE"}},
		// past the verb position: pragma names must not repeat
		{"PRAGMA journal_mode = ", nil},
		{"PRAGMA main.", nil},
		{"ATTACH 'f.db' AS ", nil},
		{"SELECT * FROM ", nil},
	}
	for _, tc := range cases {
		cands, _, ok := c.DoRepl([]rune(tc.line), len(tc.line))
		var got []string
		if ok {
			for _, cand := range cands {
				got = append(got, cand.Text)
			}
		}
		if tc.want == nil {
			if ok && len(got) != 0 {
				t.Errorf("DoRepl(%q) = %v, want no statement-word candidates", tc.line, got)
			}
			continue
		}
		if len(got) == 0 {
			t.Errorf("DoRepl(%q) = no candidates, want %v", tc.line, tc.want)
			continue
		}
		for _, w := range tc.want {
			found := false
			for _, g := range got {
				if g == w {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("DoRepl(%q) = %v, want %v among them", tc.line, got, w)
			}
		}
	}
}

// TestStatementWordsInsertDeletePrecedence: the mandatory-keyword verbs
// (INSERT INTO, DELETE FROM) keep their rule, unaffected by a statement
// words registration for another verb.
func TestStatementWordsInsertDeletePrecedence(t *testing.T) {
	c := NewDefaultCompleter(WithStatementWords(map[string][]string{
		"PRAGMA": {"journal_mode"},
	})).(*completer)
	for line, want := range map[string]string{
		"INSERT ": "INTO",
		"DELETE ": "FROM",
		"PRAGMA ": "journal_mode",
	} {
		cands, _, ok := c.DoRepl([]rune(line), len(line))
		if !ok || len(cands) != 1 || cands[0].Text != want {
			t.Errorf("DoRepl(%q) = %v (ok %v), want [%s]", line, cands, ok, want)
		}
	}
}

// TestNamesVerbs: DETACH completes an attached schema name from the L1
// schema cache, like the MySQL-family USE position.
func TestNamesVerbs(t *testing.T) {
	c := NewDefaultCompleter(
		WithNamesVerbs("DETACH"),
		WithReader(candMockReader{}),
		WithContextCompletion(),
		WithLogger(discardLogger()),
	).(*completer)
	c.DoRepl([]rune("DETACH "), 7) // arms the L1 load
	waitFor(t, func() bool {
		_, ok := c.schemas()
		return ok
	})
	cands, length := c.Do([]rune("DETACH "), 7)
	var got []string
	for _, cand := range cands {
		got = append(got, cand.Text)
	}
	for _, want := range []string{"public", "other"} {
		if !containsString(got, want) {
			t.Errorf("Do(DETACH ) = %v, want %q among them", got, want)
		}
	}
	if length != 0 {
		t.Errorf("Do(DETACH ) length = %d, want 0 (empty word)", length)
	}
	// an unregistered verb keeps the keyword fallback
	if _, ok := c.schemas(); !ok {
		t.Fatal("schemas cache did not settle")
	}
}

// TestTableFunctions: set-returning functions ride in the FROM/JOIN object
// tier, and are excluded where only relations are valid (INSERT INTO).
func TestTableFunctions(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(candMockReader{}),
		WithContextCompletion(),
		WithLogger(discardLogger()),
		WithTableFunctions(
			BuiltinFunc{Name: "pragma_table_info", Args: "table"},
			BuiltinFunc{Name: "json_each", Args: "json [, path]"},
		),
	).(*completer)
	settleCompleter(t, c)

	cands, _, ok := c.DoRepl([]rune("SELECT * FROM pragma_ta"), len("SELECT * FROM pragma_ta"))
	if !ok {
		t.Fatal("DoRepl(FROM pragma_ta) declined")
	}
	found := false
	for _, cand := range cands {
		if cand.Text == "pragma_table_info(" {
			found = true
		}
	}
	if !found {
		t.Errorf("DoRepl(FROM pragma_ta) = %v, want pragma_table_info( among them", cands)
	}

	// INSERT INTO cannot take functions
	cands, _, _ = c.DoRepl([]rune("INSERT INTO pragma_ta"), len("INSERT INTO pragma_ta"))
	for _, cand := range cands {
		if cand.Text == "pragma_table_info(" {
			t.Errorf("INSERT INTO offered table function %q", cand.Text)
		}
	}
}

// TestExtraSQLCommands: dialect keywords extend the mid-statement fallback.
func TestExtraSQLCommands(t *testing.T) {
	c := NewDefaultCompleter(WithExtraSQLCommands("GLOB", "REGEXP")).(*completer)
	cands, _ := c.Do([]rune("SELECT 1 WHERE "), len("SELECT 1 WHERE "))
	var got []string
	for _, cand := range cands {
		got = append(got, cand.Text)
	}
	for _, want := range []string{"GLOB", "REGEXP", "AND"} {
		if !containsString(got, want) {
			t.Errorf("Do(SELECT 1 WHERE ) = %v, want %q among them", got, want)
		}
	}
}
