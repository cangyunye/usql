package orshared

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	orameta "github.com/xo/usql/drivers/metadata/oracle"
	"github.com/xo/usql/rline"

	sqlite "modernc.org/sqlite"
)

// fakeDB satisfies drivers.DB without ever touching a server: the wiring
// under test only builds the completer and serves statement-start keywords,
// neither of which queries.
type fakeDB struct {
	*sql.DB
}

// fakeReader satisfies metadata.Reader (an empty interface) without
// querying.
type fakeReader struct{}

// fakeNewReader is a newReader constructor for tests that never query.
func fakeNewReader(drivers.DB, ...metadata.ReaderOption) metadata.Reader {
	return fakeReader{}
}

// testDriverOnce guards driver registration for repeated test runs
// (-count): the drivers registry is global to the binary.
var (
	testDriverMu   sync.Mutex
	testDriverDone = map[string]bool{}
)

func registerTestDriver(t *testing.T, name string, newReader func(drivers.DB, ...metadata.ReaderOption) metadata.Reader) drivers.Driver {
	t.Helper()
	testDriverMu.Lock()
	defer testDriverMu.Unlock()
	if !testDriverDone[name] {
		Register(name,
			func(err error) (string, string) { return "", "" },
			func(err error) bool { return false },
			newReader,
		)
		testDriverDone[name] = true
	}
	d, ok := drivers.Available()[name]
	if !ok {
		t.Fatalf("driver %s not registered", name)
	}
	return d
}

// TestRegisteredCompleterOffersOracleStatementStarters: the driver must wire
// its own completer so Oracle's statement starters — MERGE above all — are
// offered at statement start, instead of falling back to the generic
// PostgreSQL-flavored keyword list that lacks them.
func TestRegisteredCompleterOffersOracleStatementStarters(t *testing.T) {
	d := registerTestDriver(t, "orat", fakeNewReader)
	if d.NewCompleter == nil {
		t.Fatal("driver orat has no NewCompleter: Oracle dialect keywords never reach the completer")
	}
	c := d.NewCompleter(fakeDB{})
	got, _ := c.Do([]rune("MER"), 3)
	for _, cand := range got {
		if cand.Text == "GE" {
			return
		}
	}
	t.Errorf("Do(MER) = %v, want a MERGE suffix among candidates", got)
}

// TestRegisteredCompleterOffersOracleFunctions: the wired completer serves
// Oracle's everyday expression functions — NVL, DECODE, TO_CHAR — which the
// dialect-neutral core list lacks.
func TestRegisteredCompleterOffersOracleFunctions(t *testing.T) {
	c := registerTestDriver(t, "oratfn", fakeNewReader).NewCompleter(fakeDB{})
	rep, ok := c.(rline.Replacer)
	if !ok {
		t.Fatal("wired completer does not implement rline.Replacer")
	}
	got, _, ok := rep.DoRepl([]rune("SELECT NV"), 9)
	if !ok {
		t.Fatal("DoRepl declined the SELECT position")
	}
	for _, cand := range got {
		if cand.Text == "NVL(" {
			return
		}
	}
	t.Errorf("DoRepl(SELECT NV) = %v, want NVL( among candidates", got)
}

// sysContextOnce stands in for Oracle's SYS_CONTEXT once, globally: the
// harness's session always resolves to SCOTT.
var sysContextOnce sync.Once

func registerSysContextStub() {
	sysContextOnce.Do(func() {
		sqlite.MustRegisterDeterministicScalarFunction("SYS_CONTEXT", 2,
			func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
				return "SCOTT", nil
			})
	})
}

// openCatalogDB opens an in-memory database standing in for an Oracle-family
// server's completion catalogs: all_users (SCOTT is the current schema, ADAM
// an ordinary second user, HR an account the login holds no privilege on) and
// all_objects plus all_sequences for SCOTT's objects.
func openCatalogDB(t *testing.T) *sql.DB {
	t.Helper()
	registerSysContextStub()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// :memory: is per-connection: pin the pool to one connection, so the
	// completer's concurrent catalog loads see the tables created below
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE all_users (username text)`,
		`CREATE TABLE all_objects (owner text, object_name text, object_type text)`,
		`CREATE TABLE all_sequences (sequence_owner text, sequence_name text)`,
		`CREATE TABLE all_synonyms (owner text, synonym_name text, table_owner text, table_name text, db_link text)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][2]string{{"SYS", ""}, {"ADAM", ""}, {"SCOTT", ""}, {"HR", ""}} {
		if _, err := db.Exec(`INSERT INTO all_users VALUES (?)`, row[0]); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][3]string{
		{"SCOTT", "EMPLOYEES", "TABLE"},
		{"SCOTT", "FILM_SYN", "SYNONYM"},
		{"ADAM", "ORDERS", "TABLE"},
	} {
		if _, err := db.Exec(`INSERT INTO all_objects VALUES (?, ?, ?)`, row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO all_sequences VALUES ('SCOTT', 'EMPLOYEES_SEQ')`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestCompleterServesCurrentSchemaObjectsWithUserKind drives the wired
// completer end to end against the catalog stand-in: the FROM position must
// offer the session schema's own objects (the completer resolves the default
// schema through OnlyVisible) and namespaces must carry the "user" kind.
func TestCompleterServesCurrentSchemaObjectsWithUserKind(t *testing.T) {
	c := registerTestDriver(t, "oratlive", orameta.NewReaderQ()).NewCompleter(openCatalogDB(t),
		completer.WithContextCompletion(),
		completer.WithLogger(log.New(io.Discard, "", 0)),
	)
	rep, ok := c.(rline.Replacer)
	if !ok {
		t.Fatal("wired completer does not implement rline.Replacer")
	}

	// poll the FROM position until the lazy catalog loads land, mirroring
	// the UI's kick-driven re-render
	deadline := time.Now().Add(5 * time.Second)
	var cands []rline.Cand
	for time.Now().Before(deadline) {
		var ok bool
		cands, _, ok = rep.DoRepl([]rune("SELECT * FROM "), 14)
		if ok && hasCand(cands, "SCOTT.EMPLOYEES") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !hasCand(cands, "SCOTT.EMPLOYEES") {
		t.Fatalf("DoRepl(FROM ) = %v, want SCOTT.EMPLOYEES (the current schema's own table)", candTexts(cands))
	}
	// synonyms of the current schema complete as selectable relations
	if !hasCand(cands, "SCOTT.FILM_SYN") {
		t.Errorf("DoRepl(FROM ) = %v, want SCOTT.FILM_SYN", candTexts(cands))
	}
	// namespaces are flagged as users, not schemas
	for _, cand := range cands {
		if cand.Text == "ADAM" && cand.Kind != "user" {
			t.Errorf("ADAM kind = %q, want \"user\"", cand.Kind)
		}
	}
	// HR exists as an account but owns nothing the login can reach: the
	// schema tier (all_objects-derived, OnlyAccessible) must not offer it —
	// picking it would only ever complete into an empty menu
	for _, cand := range cands {
		if cand.Text == "HR" {
			t.Errorf("FROM menu offers HR, a namespace with no accessible objects: %v", candTexts(cands))
		}
	}
}

func hasCand(cands []rline.Cand, text string) bool {
	for _, c := range cands {
		if c.Text == text {
			return true
		}
	}
	return false
}

func candTexts(cands []rline.Cand) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Text)
	}
	return out
}
