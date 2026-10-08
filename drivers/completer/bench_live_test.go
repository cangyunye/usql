package completer_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"

	// the default driver set, so the bench can connect to real databases
	_ "github.com/xo/usql/internal"
)

// TestBenchLiveCatalog measures typing-time completion against a real,
// large catalog. Enabled only when USQL_BENCH_DSN is set (comma-separated
// DSNs); each connection reports:
//
//	cold    — connect to first non-empty DoLive candidates (lazy cache load)
//	typing  — per-keystroke DoLive latency while extending one word
//	retype  — per-keystroke latency after the word completed
//	erase   — per-backspace latency (candidate set re-filter)
//	tab     — synchronous Do latency (TAB path)
//	list    — \dt argument completion: candidate count and latency
//	menu    — FROM position: candidate count (namespaces + selectables)
func TestBenchLiveCatalog(t *testing.T) {
	dsns := os.Getenv("USQL_BENCH_DSN")
	if dsns == "" {
		t.Skip("USQL_BENCH_DSN not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		dsn = strings.TrimSpace(dsn)
		if dsn != "" {
			t.Run(dsn, func(t *testing.T) { benchDSN(t, dsn) })
		}
	}
}

func benchDSN(t *testing.T, dsn string) {
	u, err := dburl.Parse(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	db, err := sql.Open(u.Driver, u.DSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	comp := drivers.NewCompleter(ctx, u, db, nil,
		completer.WithContextCompletion(),
		completer.WithConnStrings(nil),
	)
	live := completer.NewLive(comp)

	// cold: poll DoLive at a table-position word until candidates land
	start := time.Now()
	var cold time.Duration
	line := []rune("SELECT * FROM t")
	for {
		cands, _, _ := live.(rline.LiveCompleter).DoLive(line, len(line))
		if len(cands) > 0 {
			cold = time.Since(start)
			break
		}
		if time.Since(start) > 30*time.Second {
			t.Fatal("no candidates within 30s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("cold    first candidates after %v", cold)

	// settle: the full snapshot (all reachable schemas) lands after the
	// first fall-through candidates; give it time so the numbers below are
	// the steady-state ones
	time.Sleep(2 * time.Second)

	// typing: extend the word one rune at a time; report the slowest and the
	// keystroke after the memo fills
	word := "_0001"
	worst, total := time.Duration(0), time.Duration(0)
	for i := 1; i <= len(word); i++ {
		l := append([]rune(nil), line...)
		l = append(l, []rune(word[:i])...)
		t0 := time.Now()
		live.(rline.LiveCompleter).DoLive(l, len(l))
		d := time.Since(t0)
		if d > worst {
			worst = d
		}
		total += d
		time.Sleep(20 * time.Millisecond) // let the background compute land
	}
	t.Logf("typing  worst %v, avg %v over %d keystrokes", worst, total/time.Duration(len(word)), len(word))

	// retype: the memo is hot now; keystrokes must be pure in-process work
	l := append([]rune(nil), line...)
	l = append(l, []rune(word)...)
	worst, total = 0, 0
	for i := len(word); i >= 3; i-- { // erase: backspaces re-filter the memo
		ll := append([]rune(nil), line...)
		ll = append(ll, []rune(word[:i])...)
		t0 := time.Now()
		cands, _, _ := live.(rline.LiveCompleter).DoLive(ll, len(ll))
		d := time.Since(t0)
		if d > worst {
			worst = d
		}
		total += d
		if i == len(word) {
			t.Logf("menu    %d candidates for %q", len(cands), word)
		}
	}
	t.Logf("erase   worst %v, avg %v over %d backspaces", worst, total/time.Duration(len(word)-2), len(word)-2)

	// tab: synchronous path
	t0 := time.Now()
	cands, _ := live.Do(append(append([]rune(nil), line...), []rune("_0001")...), len(line)+len(word))
	t.Logf("tab     %d candidates in %v", len(cands), time.Since(t0))

	// \dt listing
	dt := []rune(`\dt `)
	t0 = time.Now()
	cands, _, _ = comp.(rline.Replacer).DoRepl(dt, len(dt))
	t.Logf("list    \\dt -> %d candidates in %v", len(cands), time.Since(t0))

	dt = []rune(`\dt t_01`)
	t0 = time.Now()
	cands, _, _ = comp.(rline.Replacer).DoRepl(dt, len(dt))
	t.Logf("list    \\dt t_01 -> %d candidates in %v", len(cands), time.Since(t0))

	// FROM position: the full option set size (namespaces + selectables)
	fr := []rune("SELECT * FROM ")
	t0 = time.Now()
	cands, _, _ = comp.(rline.Replacer).DoRepl(fr, len(fr))
	t.Logf("menu    FROM -> %d candidates (namespaces first) in %v", len(cands), time.Since(t0))

	fmt.Println()
}

// --- single-connection contention bench (Phase 0 of
// docs/topics/completion-conn-contention.md) ---

// benchSlowReader wraps the real metadata reader, optionally holding the
// pool's connection for delay before each catalog query — the deterministic
// stand-in for a slow catalog (an OceanBase cold metadata query measured
// ~10.5s in TestBenchLiveCatalog). It counts the queries in flight, so a
// test can wait until a load actually owns the connection.
type benchSlowReader struct {
	metadata.Reader

	db    *sql.DB
	sleep string        // server-side sleep statement, %s = seconds
	delay time.Duration // hold time before each query; 0 disables

	mu       sync.Mutex
	inflight int
	schemas  int   // completed Schemas calls since the last reset
	holdErr  error // first failure of the artificial hold, if any
}

func (r *benchSlowReader) enter() {
	r.mu.Lock()
	r.inflight++
	r.mu.Unlock()
	if r.delay > 0 {
		q := fmt.Sprintf(r.sleep, strconv.FormatFloat(r.delay.Seconds(), 'f', -1, 64))
		if _, err := r.db.ExecContext(context.Background(), q); err != nil && r.holdErr == nil {
			r.mu.Lock()
			r.holdErr = err
			r.mu.Unlock()
		}
	}
}

func (r *benchSlowReader) exit() {
	r.mu.Lock()
	r.inflight--
	r.mu.Unlock()
}

func (r *benchSlowReader) busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inflight > 0
}

func (r *benchSlowReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	r.enter()
	defer r.exit()
	set, err := r.Reader.(metadata.SchemaReader).Schemas(f)
	r.mu.Lock()
	r.schemas++
	r.mu.Unlock()
	return set, err
}

func (r *benchSlowReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	r.enter()
	defer r.exit()
	return r.Reader.(metadata.TableReader).Tables(f)
}

func (r *benchSlowReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	r.enter()
	defer r.exit()
	return r.Reader.(metadata.ColumnReader).Columns(f)
}

func (r *benchSlowReader) Functions(f metadata.Filter) (*metadata.FunctionSet, error) {
	r.enter()
	defer r.exit()
	return r.Reader.(metadata.FunctionReader).Functions(f)
}

func (r *benchSlowReader) Sequences(f metadata.Filter) (*metadata.SequenceSet, error) {
	r.enter()
	defer r.exit()
	return r.Reader.(metadata.SequenceReader).Sequences(f)
}

// TestBenchLiveContention measures the two directions of the
// single-connection contention between the CLI's user statements and the
// completion cache's background metadata loads. Enabled when USQL_BENCH_DSN
// is set (comma-separated DSNs). drivers.Open pins the pool to one
// connection, so both directions queue on it:
//
//	load — completion load landing latency: idle, during a 2s user
//	       statement on the pinned pool, and during the same statement
//	       with a second, metadata-only connection (the option-A shape)
//	stmt — user statement latency (Enter→first row): idle, while a
//	       metadata load holds the pinned pool's connection (a controlled
//	       0.5s slow-catalog stand-in), and the same load on the second
//	       connection
//
// The 2s user statement is a server-side sleep (pg_sleep / SLEEP /
// dbms_session.sleep); the slow metadata query holds the connection the
// same way a real cold-catalog query does.
func TestBenchLiveContention(t *testing.T) {
	dsns := os.Getenv("USQL_BENCH_DSN")
	if dsns == "" {
		t.Skip("USQL_BENCH_DSN not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		dsn = strings.TrimSpace(dsn)
		if dsn != "" {
			t.Run(dsn, func(t *testing.T) { contentionDSN(t, dsn) })
		}
	}
}

// contentionSQL returns the driver's server-side sleep statement ("%s" is
// the seconds) and a trivial statement probing user-statement latency.
func contentionSQL(driver string) (sleep, probe string, ok bool) {
	switch driver {
	case "postgres":
		return "SELECT pg_sleep(%s)", "SELECT 1", true
	case "mysql", "mymysql":
		return "SELECT SLEEP(%s)", "SELECT 1", true
	case "oracle", "godror":
		return "BEGIN dbms_session.sleep(%s); END;", "SELECT 1 FROM DUAL", true
	}
	return "", "", false
}

// openPinned opens the DSN with the pool pinned exactly like drivers.Open.
func openPinned(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

// seconds renders d as the shortest decimal seconds string.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

// startSlowStatement issues the server-side sleep in the background and
// returns only after it holds the pool's connection — the stand-in for the
// user having pressed Enter on a long statement. The returned func waits
// for the statement to finish.
func startSlowStatement(t *testing.T, db *sql.DB, sleep string, d time.Duration) (done func()) {
	t.Helper()
	q := fmt.Sprintf(sleep, seconds(d))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ch := make(chan error, 1)
	go func() {
		_, err := db.ExecContext(ctx, q)
		ch <- err
	}()
	waitInUse(t, db)
	return func() {
		defer cancel()
		if err := <-ch; err != nil {
			t.Errorf("slow statement: %v", err)
		}
	}
}

// waitInUse fails the test unless the pool's connection is checked out
// within 5s.
func waitInUse(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if db.Stats().InUse >= 1 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no query ever took the pool's connection")
}

// waitForQuiet fails the test unless every metadata load finished and the
// pool's connection is back within 5s.
func waitForQuiet(t *testing.T, r *benchSlowReader, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !r.busy() && db.Stats().InUse == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("metadata activity never settled")
}

// loadLanding invalidates the cache and polls the bare FROM completion
// until the L1 schema loads behind it land — the same cache and pool serve
// the L2/L3 loads. The keyword tier always offers candidates, so the
// landing signal is the reader's completed Schemas-call count (both L1
// entries: all + current), the way the contract test detects landings.
func loadLanding(t *testing.T, live rline.LiveCompleter, inv interface{ Invalidate() }, r *benchSlowReader, db *sql.DB) time.Duration {
	t.Helper()
	r.mu.Lock()
	r.schemas = 0
	r.mu.Unlock()
	inv.Invalidate()
	line := []rune("SELECT * FROM ")
	start := time.Now()
	deadline := start.Add(30 * time.Second)
	for {
		live.DoLive(line, len(line))
		r.mu.Lock()
		landed := r.schemas >= 2
		r.mu.Unlock()
		if landed {
			return time.Since(start)
		}
		if time.Now().After(deadline) {
			t.Fatal("completion schema loads never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// probeLatency measures user-statement latency: submit the trivial probe
// and time it to the first row — the Enter→first-byte experience.
func probeLatency(t *testing.T, db *sql.DB, probe string) time.Duration {
	t.Helper()
	t0 := time.Now()
	var n any
	if err := db.QueryRow(probe).Scan(&n); err != nil {
		t.Fatalf("probe %q: %v", probe, err)
	}
	return time.Since(t0)
}

func contentionDSN(t *testing.T, dsn string) {
	u, err := dburl.Parse(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	driver := u.Driver
	if u.GoDriver != "" {
		driver = u.GoDriver
	}
	sleepQ, probeQ, ok := contentionSQL(u.Driver)
	if !ok {
		t.Skipf("no deterministic server-side sleep for %q", u.Driver)
	}

	// the user-statement pool, pinned exactly like drivers.Open ...
	db := openPinned(t, driver, u.DSN)
	// ... and the metadata-only pool of the option-A shape: same DSN, its
	// own single-connection pool
	metaDB := openPinned(t, driver, u.DSN)

	if u.Driver == "oracle" {
		// dbms_session.sleep is public since 18c; fall back to dbms_lock
		if _, err := db.Exec(fmt.Sprintf(sleepQ, "0.2")); err != nil {
			first := err
			alt := "BEGIN dbms_lock.sleep(%s); END;"
			_, err = db.Exec(fmt.Sprintf(alt, "0.2"))
			if err != nil {
				t.Skipf("no usable server-side sleep: %v / %v", first, err)
			}
			sleepQ = alt
		}
	}

	kind := "schema"
	switch u.Driver {
	case "mysql", "mymysql":
		kind = "database"
	case "oracle", "godror":
		kind = "user"
	}

	newCompleter := func(meta *sql.DB) (rline.LiveCompleter, interface{ Invalidate() }, *benchSlowReader) {
		real, err := drivers.NewMetadataReader(context.Background(), u, meta, io.Discard,
			metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
		if err != nil {
			t.Fatalf("metadata reader: %v", err)
		}
		wrapped := &benchSlowReader{Reader: real, db: meta, sleep: sleepQ}
		c := completer.NewDefaultCompleter(
			completer.WithReader(wrapped),
			completer.WithDB(meta),
			completer.WithSchemaKind(kind),
			completer.WithContextCompletion(),
		)
		live := completer.NewLive(c).(rline.LiveCompleter)
		return live, live.(interface{ Invalidate() }), wrapped
	}
	live, inv, wrapped := newCompleter(db)              // pinned: metadata on the user pool
	sepLive, sepInv, sepWrapped := newCompleter(metaDB) // option-A shape: own pool

	// a schema-qualified line arms exactly one L2 load (candidates.go's
	// qualifiedOptions): the controlled "load in flight" kick below
	schema := ""
	if tr, ok := wrapped.Reader.(metadata.TableReader); ok {
		set, err := tr.Tables(metadata.Filter{WithSystem: false})
		if err != nil {
			t.Fatalf("discovery: %v", err)
		}
		if set.Next() {
			schema = set.Get().Schema
		}
		set.Close()
	}
	if schema == "" {
		if u.Driver == "oracle" || u.Driver == "godror" {
			schema = u.User.Username()
		} else {
			t.Skip("catalog has no visible schema; no qualified kick")
		}
	}
	t.Logf("qualified L2 kick target: %s.", schema)

	// --- load landing latency vs a slow user statement ---
	base := loadLanding(t, live, inv, wrapped, db)
	t.Logf("load    idle baseline            %v", base.Round(time.Millisecond))
	waitForQuiet(t, wrapped, db)

	done := startSlowStatement(t, db, sleepQ, 2*time.Second)
	lat := loadLanding(t, live, inv, wrapped, db)
	t.Logf("load    during 2s stmt (1 conn)  %v", lat.Round(time.Millisecond))
	done()
	waitForQuiet(t, wrapped, db)

	done = startSlowStatement(t, db, sleepQ, 2*time.Second)
	sepLat := loadLanding(t, sepLive, sepInv, sepWrapped, metaDB)
	t.Logf("load    during 2s stmt (2 conn)  %v", sepLat.Round(time.Millisecond))
	done()
	waitForQuiet(t, wrapped, db)
	waitForQuiet(t, sepWrapped, metaDB)

	// --- user statement latency vs a slow metadata load ---
	stmtBase := probeLatency(t, db, probeQ)
	t.Logf("stmt    idle baseline            %v", stmtBase.Round(time.Microsecond))

	wrapped.delay = 500 * time.Millisecond
	qline := []rune("SELECT * FROM " + schema + ".")
	inv.Invalidate()
	live.DoLive(qline, len(qline))
	waitInUse(t, db)
	stmtLat := probeLatency(t, db, probeQ)
	t.Logf("stmt    during load (1 conn)     %v", stmtLat.Round(time.Millisecond))
	wrapped.delay = 0
	waitForQuiet(t, wrapped, db)

	sepWrapped.delay = 500 * time.Millisecond
	sepInv.Invalidate()
	sepLive.DoLive(qline, len(qline))
	waitInUse(t, metaDB)
	sepStmtLat := probeLatency(t, db, probeQ)
	t.Logf("stmt    during load (2 conn)     %v", sepStmtLat.Round(time.Millisecond))
	sepWrapped.delay = 0
	waitForQuiet(t, sepWrapped, metaDB)

	if wrapped.holdErr != nil {
		t.Errorf("artificial metadata hold failed (1-conn numbers are baseline-only): %v", wrapped.holdErr)
	}
	if sepWrapped.holdErr != nil {
		t.Errorf("artificial metadata hold failed (2-conn numbers are baseline-only): %v", sepWrapped.holdErr)
	}
}
