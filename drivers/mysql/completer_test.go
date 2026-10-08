package mysql

import (
	"database/sql"
	"testing"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/rline"
)

// fakeDB satisfies drivers.DB without ever touching a server: the wiring
// under test only builds the completer and serves statement-start keywords
// and static functions, neither of which queries.
type fakeDB struct {
	*sql.DB
}

// TestMySQLCompleterDialect: the driver layers the MySQL dialect onto the
// shared MySQL-family completer — REPLACE and friends at statement start,
// and the everyday expression functions (IFNULL, NOW, ...) the neutral core
// list lacks.
func TestMySQLCompleterDialect(t *testing.T) {
	d, ok := drivers.Available()["mysql"]
	if !ok {
		t.Fatal("driver mysql not registered")
	}
	c := d.NewCompleter(fakeDB{})

	got, _ := c.Do([]rune("REPLA"), 5)
	for _, cand := range got {
		if cand.Text == "CE" {
			return
		}
	}
	t.Errorf("Do(REPLA) = %v, want a REPLACE suffix among candidates", got)
}

// TestMySQLCompleterFunctions: expression positions offer MySQL's everyday
// functions.
func TestMySQLCompleterFunctions(t *testing.T) {
	c := drivers.Available()["mysql"].NewCompleter(fakeDB{})
	rep, ok := c.(rline.Replacer)
	if !ok {
		t.Fatal("wired completer does not implement rline.Replacer")
	}
	got, _, ok := rep.DoRepl([]rune("SELECT IFNU"), 11)
	if !ok {
		t.Fatal("DoRepl declined the SELECT position")
	}
	for _, cand := range got {
		if cand.Text == "IFNULL(" {
			return
		}
	}
	t.Errorf("DoRepl(SELECT IFNU) = %v, want IFNULL( among candidates", got)
}
