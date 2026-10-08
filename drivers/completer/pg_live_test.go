package completer_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	_ "github.com/xo/usql/internal"
	"github.com/xo/usql/rline"
)

// openPGLiveDB opens the USQL_LIVE_PG connection (keyword DSN) with its
// metadata reader.
func openPGLiveDB(t *testing.T) (*sql.DB, *dburl.URL, metadata.Reader) {
	t.Helper()
	dsn := os.Getenv("USQL_LIVE_PG")
	if dsn == "" {
		t.Skip("USQL_LIVE_PG not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// SET search_path below is session-scoped: pin the pool to one
	// connection, so the completer's queries see the same session GUCs
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	u := &dburl.URL{Driver: "postgres"}
	real, err := drivers.NewMetadataReader(context.Background(), u, db, io.Discard,
		metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
	if err != nil {
		t.Fatal(err)
	}
	return db, u, real
}

// TestLivePGCasePreservation: a lowercase prefix must offer the mixed-case
// table with its stored case — the case-mirroring rule keeps catalog
// candidates' stored case, which quoted identifiers require.
func TestLivePGCasePreservation(t *testing.T) {
	db, u, _ := openPGLiveDB(t)
	comp := drivers.NewCompleter(context.Background(), u, db, nil,
		completer.WithContextCompletion(),
		completer.WithConnStrings(nil),
	)
	live := completer.NewLive(comp).(rline.LiveCompleter)
	line := []rune("SELECT * FROM de")
	deadline := time.Now().Add(15 * time.Second)
	var found string
	for time.Now().Before(deadline) {
		cands, _, _ := live.DoLive(line, len(line))
		for _, c := range cands {
			if c.Text == "public.DEPT" {
				found = c.Text
			}
		}
		if found != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if found == "" {
		t.Fatal("lowercase prefix de did not offer public.DEPT with stored case")
	}
	t.Logf("lowercase %q -> %q (stored case preserved)", "de", found)
}

// TestLivePGLikeLeak: the current schema's completion scope must not leak
// look-alike schemas (same length, differing at an underscore) — the L3
// column load resolves via the exact current-schema match, and the L2
// bare-object menu merges the visibility tier.
func TestLivePGLikeLeak(t *testing.T) {
	db, u, real := openPGLiveDB(t)
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS my_schema CASCADE`,
		`DROP SCHEMA IF EXISTS myxschema CASCADE`,
		`CREATE SCHEMA my_schema`,
		`CREATE SCHEMA myxschema`,
		`CREATE TABLE my_schema.orders (id int)`,
		`CREATE TABLE myxschema.orders (id int, extra int)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() {
		db.Exec(`DROP SCHEMA IF EXISTS my_schema CASCADE`)
		db.Exec(`DROP SCHEMA IF EXISTS myxschema CASCADE`)
	})

	// L3: the bare column load resolves via the exact current schema
	if _, err := db.Exec(`SET search_path = my_schema`); err != nil {
		t.Fatal(err)
	}
	cset, err := real.(metadata.ColumnReader).Columns(metadata.Filter{Parent: "orders", OnlyVisible: true})
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	var cols []string
	for cset.Next() {
		col := cset.Get()
		cols = append(cols, col.Schema+"."+col.Table+"."+col.Name)
	}
	cset.Close()
	if len(cols) != 1 || cols[0] != "my_schema.orders.id" {
		t.Errorf("LIKE leak: Columns(orders, OnlyVisible) = %v, want [my_schema.orders.id]", cols)
	}

	// L2: the bare-object menu merges the current schema's objects through
	// the visibility tier — the look-alike schema's table must not appear
	comp := drivers.NewCompleter(context.Background(), u, db, nil,
		completer.WithContextCompletion(),
		completer.WithConnStrings(nil),
	)
	if inv, ok := comp.(interface{ Invalidate() }); ok {
		inv.Invalidate() // the search_path changed the completion scope
	}
	live := completer.NewLive(comp).(rline.LiveCompleter)
	line := []rune("SELECT * FROM ")
	var cands []rline.Cand
	deadline := time.Now().Add(15 * time.Second)
	stable := 0
	for time.Now().Before(deadline) && stable < 40 {
		next, _, _ := live.DoLive(line, len(line))
		if len(next) == len(cands) {
			stable++
		} else {
			stable = 0
			cands = next
		}
		time.Sleep(5 * time.Millisecond)
	}
	var hasOwn, hasLeak bool
	for _, c := range cands {
		switch c.Text {
		case "my_schema.orders":
			hasOwn = true
		case "myxschema.orders":
			hasLeak = true
		}
	}
	if !hasOwn {
		t.Errorf("FROM menu missing my_schema.orders (got %d candidates)", len(cands))
	}
	if hasLeak {
		t.Errorf("LIKE leak: myxschema.orders in the current schema's object menu")
	}
}
