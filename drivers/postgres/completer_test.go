package postgres

import (
	"database/sql"
	"testing"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/rline"
)

// fakeDB satisfies drivers.DB without ever touching a server: the wiring
// under test only builds the completer and serves statement keywords and
// static functions, neither of which queries.
type fakeDB struct {
	*sql.DB
}

// TestPostgresCompleterDialect: the driver layers the PostgreSQL dialect onto
// the completer — mid-statement keywords (ILIKE, ON CONFLICT, ...) the
// common list lacks.
func TestPostgresCompleterDialect(t *testing.T) {
	d, ok := drivers.Available()["postgres"]
	if !ok {
		t.Fatal("driver postgres not registered")
	}
	if d.NewCompleter == nil {
		t.Fatal("driver postgres has no NewCompleter: PostgreSQL dialect keywords never reach the completer")
	}
	c := d.NewCompleter(fakeDB{})
	got, _ := c.Do([]rune("SELECT x ILI"), 12)
	for _, cand := range got {
		if cand.Text == "KE" {
			return
		}
	}
	t.Errorf("Do(ILU) = %v, want an ILIKE suffix among candidates", got)
}

// TestPostgresCompleterFunctions: expression positions offer PostgreSQL's
// everyday functions, and FROM positions its set-returning ones.
func TestPostgresCompleterFunctions(t *testing.T) {
	c := drivers.Available()["postgres"].NewCompleter(fakeDB{})
	rep, ok := c.(rline.Replacer)
	if !ok {
		t.Fatal("wired completer does not implement rline.Replacer")
	}
	got, _, ok := rep.DoRepl([]rune("SELECT date_t"), 13)
	if !ok {
		t.Fatal("DoRepl declined the SELECT position")
	}
	for _, cand := range got {
		if cand.Text == "date_trunc(" {
			return
		}
	}
	t.Errorf("DoRepl(SELECT date_t) = %v, want date_trunc( among candidates", got)

	got2, _, _ := rep.DoRepl([]rune("SELECT * FROM generate_"), 23)
	for _, cand := range got2 {
		if cand.Text == "generate_series(" {
			return
		}
	}
	t.Errorf("DoRepl(FROM generate_) = %v, want generate_series( among candidates", got2)
}
