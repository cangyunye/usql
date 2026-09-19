package completer_test

import (
	"context"
	"database/sql"
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"

	// the default driver set, so the contract can connect to real databases
	_ "github.com/xo/usql/internal"
)

// liveCountReader counts catalog calls per method, delegating to the real
// driver reader.
type liveCountReader struct {
	metadata.Reader

	mu     sync.Mutex
	counts map[string]int
}

func newLiveCountReader(real metadata.Reader) *liveCountReader {
	return &liveCountReader{Reader: real, counts: map[string]int{}}
}

func (r *liveCountReader) bump(op string) {
	r.mu.Lock()
	r.counts[op]++
	r.mu.Unlock()
}

func (r *liveCountReader) calls(op string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[op]
}

func (r *liveCountReader) reset() {
	r.mu.Lock()
	r.counts = map[string]int{}
	r.mu.Unlock()
}

func (r *liveCountReader) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.counts {
		n += v
	}
	return n
}

func (r *liveCountReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	r.bump("schemas")
	return r.Reader.(metadata.SchemaReader).Schemas(f)
}

func (r *liveCountReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	r.bump("tables")
	return r.Reader.(metadata.TableReader).Tables(f)
}

func (r *liveCountReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	r.bump("columns")
	return r.Reader.(metadata.ColumnReader).Columns(f)
}

func (r *liveCountReader) Functions(f metadata.Filter) (*metadata.FunctionSet, error) {
	r.bump("functions")
	return r.Reader.(metadata.FunctionReader).Functions(f)
}

func (r *liveCountReader) Sequences(f metadata.Filter) (*metadata.SequenceSet, error) {
	r.bump("sequences")
	return r.Reader.(metadata.SequenceReader).Sequences(f)
}

// TestLiveCompletionContract runs the lazy-cache contract against real
// databases (skipped when the env DSNs are unset):
//
//	USQL_LIVE_MYSQL   go-sql-driver DSN   root:pw@tcp(host:port)/db
//	USQL_LIVE_PG      keyword DSN         host=... user=... password=... dbname=...
//	USQL_LIVE_ORACLE  URL DSN             oracle://user:pw@host:port/service
//
// Contract: connecting issues zero metadata queries; the first FROM
// completion loads L1 (2 schema queries) plus the current schema's objects
// (1 tables query); "schema." loads that namespace once; "table." loads its
// columns once; repeats are free; invalidation re-queries.
func TestLiveCompletionContract(t *testing.T) {
	ran := false
	if dsn := os.Getenv("USQL_LIVE_MYSQL"); dsn != "" {
		ran = true
		t.Run("mysql", func(t *testing.T) {
			runLiveContract(t, "mysql", "database", dsn, "performance_schema", "")
		})
	}
	if dsn := os.Getenv("USQL_LIVE_PG"); dsn != "" {
		ran = true
		t.Run("postgres", func(t *testing.T) {
			runLiveContract(t, "postgres", "schema", dsn, "pg_catalog", "")
		})
	}
	if dsn := os.Getenv("USQL_LIVE_ORACLE"); dsn != "" {
		ran = true
		t.Run("oracle", func(t *testing.T) {
			runLiveContract(t, "oracle", "user", dsn, "", "")
		})
	}
	if !ran {
		t.Skip("USQL_LIVE_MYSQL / USQL_LIVE_PG / USQL_LIVE_ORACLE not set")
	}
}

// runLiveContract drives one live database through the lazy-cache contract.
// otherSchema (when set) is a second namespace for the L2 tier; when empty
// the L2 check is skipped.
func runLiveContract(t *testing.T, driver, kind, goDSN, otherSchema, _ string) {
	t.Helper()
	var u *dburl.URL
	switch driver {
	case "oracle":
		var err error
		if u, err = dburl.Parse(goDSN); err != nil {
			t.Fatalf("parse: %v", err)
		}
		goDSN = u.DSN
	default:
		u = &dburl.URL{Driver: driver}
	}
	db, err := sql.Open(driver, goDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t0 := time.Now()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Logf("connect (TCP+auth) in %v", time.Since(t0).Round(time.Millisecond))

	real, err := drivers.NewMetadataReader(context.Background(), u, db, io.Discard,
		metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
	if err != nil {
		t.Fatalf("metadata reader: %v", err)
	}
	// discover a schema-qualified table from the catalog itself — the
	// discovery queries run before the counting starts, so they do not
	// pollute the contract
	var qualified string
	if tr, ok := real.(metadata.TableReader); ok {
		if disc, err := tr.Tables(metadata.Filter{WithSystem: false}); err != nil {
			t.Fatalf("discovery: %v", err)
		} else {
			for disc.Next() {
				row := disc.Get()
				if row.Schema != "" && row.Name != "" {
					qualified = row.Schema + "." + row.Name
					break
				}
			}
			disc.Close()
		}
	}
	if qualified == "" {
		t.Skip("catalog has no visible tables; nothing to complete")
	}
	t.Logf("discovery L3 target: %s", qualified)

	wrapped := newLiveCountReader(real)

	c := completer.NewDefaultCompleter(
		completer.WithReader(wrapped),
		completer.WithDB(db),
		completer.WithLogger(log.New(io.Discard, "", 0)),
		completer.WithSchemaKind(kind),
		completer.WithContextCompletion(),
	)
	live := completer.NewLive(c).(rline.LiveCompleter)
	inv, ok := live.(interface{ Invalidate() })
	if !ok {
		t.Fatal("NewLive result does not implement Invalidate")
	}

	// contract 0: connecting issued no metadata queries
	if n := wrapped.total(); n != 0 {
		t.Fatalf("connect issued %d metadata queries, want 0", n)
	}

	// contract 1: the first FROM completion loads L1 (all + current = 2
	// schema queries) and the current schema's objects (1 tables query).
	// The polls re-issue the request, mirroring the UI's kick-driven
	// re-render: each landed load lets the next request arm the next level.
	fromLine := []rune("SELECT * FROM ")
	start := time.Now()
	var cands []rline.Cand
	var cold time.Duration
	req := func() {
		if res, _, _ := live.DoLive(fromLine, len(fromLine)); len(res) > 0 {
			if cold == 0 {
				cold = time.Since(start)
			}
			cands = res
		}
	}
	req()
	waitCalls(t, wrapped, req, func() bool {
		return cold != 0 && wrapped.calls("schemas") >= 2 && wrapped.calls("tables") >= 1
	})
	t.Logf("cold     first FROM candidates after %v (%d candidates)", cold.Round(time.Millisecond), len(cands))
	if n := wrapped.calls("schemas"); n != 2 {
		t.Errorf("schemas queried %d times after first FROM, want 2 (all + current)", n)
	}
	if n := wrapped.calls("tables"); n != 1 {
		t.Errorf("tables queried %d times after first FROM, want 1 (current schema objects)", n)
	}
	t.Logf("L1+L2    schemas=%d tables=%d functions=%d sequences=%d columns=%d",
		wrapped.calls("schemas"), wrapped.calls("tables"), wrapped.calls("functions"), wrapped.calls("sequences"), wrapped.calls("columns"))

	// repeats are free: the same completion re-issued must not query
	before := wrapped.total()
	for i := 0; i < 5; i++ {
		live.DoLive(fromLine, len(fromLine))
	}
	if n := wrapped.total(); n != before {
		t.Errorf("re-issuing FROM completion issued %d extra queries, want 0", n-before)
	}

	// contract 2: "schema." loads that namespace's objects — one tables
	// query per level key
	if otherSchema != "" {
		qualifiedLine := []rune("SELECT * FROM " + otherSchema + ".")
		before := wrapped.calls("tables")
		waitCalls(t, wrapped, func() { live.DoLive(qualifiedLine, len(qualifiedLine)) },
			func() bool { return wrapped.calls("tables") >= before+1 })
		if n := wrapped.calls("tables"); n != before+1 {
			t.Errorf("tables queried %d times after %s., want exactly one more", n, otherSchema)
		}
		t.Logf("L2       %s. tables=%d functions=%d sequences=%d", otherSchema, wrapped.calls("tables"), wrapped.calls("functions"), wrapped.calls("sequences"))
	}

	// contract 3: "table." loads the table's columns — one query
	colLine := []rune("SELECT * FROM " + qualified + " WHERE ")
	before = wrapped.calls("columns")
	waitCalls(t, wrapped, func() { live.DoLive(colLine, len(colLine)) },
		func() bool { return wrapped.calls("columns") >= before+1 })
	if n := wrapped.calls("columns"); n != before+1 {
		t.Errorf("columns queried %d times after %s., want exactly one more", n, qualified)
	}
	// repeats (typing more of the word) stay free
	word := colLine
	word = append(word, []rune("id")...)
	for i := range 3 {
		live.DoLive(word, len(colLine)+i)
	}
	if n := wrapped.calls("columns"); n != before+1 {
		t.Errorf("columns queried %d times after repeats, want %d", n, before+1)
	}
	t.Logf("L3       %s. columns=%d", qualified, wrapped.calls("columns"))

	// contract 4: completing inside a string literal issues nothing
	strLine := []rune("SELECT * FROM " + qualified + " WHERE name = 'x")
	if repl, ok := live.(rline.Replacer); ok {
		if cands, _, ok := repl.DoRepl(strLine, len(strLine)); ok && len(cands) > 0 {
			t.Errorf("completion inside a string literal returned %q, want nothing", cands)
		}
	}
	if got := wrapped.calls("columns"); got != before+1 {
		t.Errorf("string-literal completion issued queries: columns=%d", got)
	}

	// contract 5: invalidation (scope change / \refresh) re-queries
	inv.Invalidate()
	start = time.Now()
	req = func() { live.DoLive(fromLine, len(fromLine)) }
	waitCalls(t, wrapped, req, func() bool { return wrapped.calls("schemas") >= 4 })
	t.Logf("refresh  candidates after invalidate in %v", time.Since(start).Round(time.Millisecond))
	if n := wrapped.calls("schemas"); n != 4 {
		t.Errorf("schemas queried %d times after invalidate, want 4 (2 more)", n)
	}
	t.Logf("totals   %v", wrapped.countsSnapshot())
}

// waitCalls polls until cond passes — re-issuing req on every poll, the way
// the UI re-requests on kick — and fails the test after a deadline.
func waitCalls(t *testing.T, r *liveCountReader, req func(), cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		req()
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not reached (calls: %v)", r.countsSnapshot())
}

// countsSnapshot returns a copy of the counts for logging.
func (r *liveCountReader) countsSnapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.counts))
	for k, v := range r.counts {
		out[k] = v
	}
	return out
}
