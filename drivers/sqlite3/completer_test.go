package sqlite3

import (
	"testing"

	"github.com/xo/usql/drivers/sqlite3/sqshared"
	"github.com/xo/usql/rline"
)

// TestCompleterStatementWords exercises the sqlite3 completer's fixed-word
// tiers without a live database: statement words are static, so the PRAGMA
// and ATTACH positions resolve with no catalog at all.
func TestCompleterStatementWords(t *testing.T) {
	rp, ok := sqshared.NewCompleter(nil).(rline.Replacer)
	if !ok {
		t.Fatal("NewCompleter result does not implement rline.Replacer")
	}
	for _, tc := range []struct {
		line string
		want string
	}{
		{"PRAGMA fore", "foreign_keys"},
		{"PRAGMA journal_", "journal_mode"},
		{"PRAGMA table_i", "table_info"},
		{"PRAGMA ", "busy_timeout"},
		{"ATTACH ", "DATABASE"},
		// past the verb position nothing pragma-specific is served; the
		// generic keyword tier applies (only its absence is asserted here)
	} {
		cands, _, ok := rp.DoRepl([]rune(tc.line), len(tc.line))
		if !ok {
			t.Errorf("DoRepl(%q) declined; want %q among candidates", tc.line, tc.want)
			continue
		}
		found := false
		for _, cand := range cands {
			if cand.Text == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("DoRepl(%q) = %v, want %q among them", tc.line, cands, tc.want)
		}
	}
}

// TestCompleterStatementWordsNotLater pins the AfterVerb restriction: a
// cursor later in the PRAGMA statement must not offer pragma names again.
func TestCompleterStatementWordsNotLater(t *testing.T) {
	rp, ok := sqshared.NewCompleter(nil).(rline.Replacer)
	if !ok {
		t.Fatal("NewCompleter result does not implement rline.Replacer")
	}
	cands, _, _ := rp.DoRepl([]rune("PRAGMA journal_mode = "), len("PRAGMA journal_mode = "))
	for _, cand := range cands {
		if cand.Text == "journal_mode" || cand.Text == "foreign_keys" {
			t.Errorf("pragma name %q offered past the verb position", cand.Text)
		}
	}
}
