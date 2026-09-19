package completer

import (
	"sync"
	"testing"
	"time"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

func TestScopeChanged(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"USE other", true},
		{"use other;", true},
		{"  USE  `some-db`", true},
		{"SET search_path TO alt, public", true},
		{"set search_path=alt;", true},
		{"ALTER SESSION SET CURRENT_SCHEMA = MIGSRC", true},
		{"alter session set current_schema=MIGSRC;", true},
		{"SELECT * FROM film", false},
		{"INSERT INTO film VALUES (1)", false},
		{"SELECT 'set search_path' FROM film", false},
		{"UPDATE film SET name = 'USE'", false},
		{"BEGIN", false},
	}
	for _, c := range cases {
		if got := ScopeChanged(c.sql); got != c.want {
			t.Errorf("ScopeChanged(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// TestInvalidateDropsCache: after Invalidate, the same completion re-queries
// the reader instead of serving stale entries.
func TestInvalidateDropsCache(t *testing.T) {
	inner := newCountingReader()
	c := &completer{reader: inner, logger: discardLogger(), schemaKind: "schema"}
	WithContextCompletion()(c)
	if c.cache == nil {
		t.Fatal("WithContextCompletion did not install a cache")
	}

	c.schemaObjects("", "public")
	waitFor(t, func() bool { return inner.calls("tables") >= 1 })
	c.schemaObjects("", "public") // cached: no new query
	if n := inner.calls("tables"); n != 1 {
		t.Fatalf("inner queried %d times before invalidate, want 1", n)
	}

	c.Invalidate()
	c.schemaObjects("", "public") // re-arms the load
	waitFor(t, func() bool { return inner.calls("tables") >= 2 })
}

// TestLiveWrapper pins the thin live wrapper: synchronous delegation, kick
// wiring, and Invalidate forwarding.
func TestLiveWrapper(t *testing.T) {
	stub := &stubCompleter{}
	live := NewLive(stub)
	lc, ok := live.(rline.LiveCompleter)
	if !ok {
		t.Fatal("NewLive result does not implement LiveCompleter")
	}
	// DoLive is synchronous: the replace-style path through DoRepl first
	// (the stub is no replacer), then Do
	cands, length, replace := lc.DoLive([]rune("sel"), 3)
	if len(cands) != 1 || cands[0].Text != "ect" || length != 3 || replace {
		t.Fatalf("DoLive = %q %d %v", cands, length, replace)
	}
	// TAB path delegates unchanged
	if cands, _ := live.Do([]rune("sel"), 3); len(cands) != 1 {
		t.Fatalf("Do = %q", cands)
	}
	// Invalidate forwards without error
	if inv, ok := live.(interface{ Invalidate() }); !ok {
		t.Fatal("NewLive result does not implement Invalidate")
	} else {
		inv.Invalidate()
	}
	// SetLiveKick on a kick-less inner is a no-op, not a panic
	lc.SetLiveKick(func() {})
}

// TestSetKickWiring: the completer's SetKick reaches the cache.
func TestSetKickWiring(t *testing.T) {
	c := contextTestCompleter(t)
	fired := make(chan struct{}, 1)
	c.SetKick(func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	c.DoRepl([]rune("SELECT * FROM "), 14) // arms the L1 loads
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("kick did not fire after a lazy load")
	}
}

// stubCompleter is a minimal fixed completer.
type stubCompleter struct{}

func (stubCompleter) Do(line []rune, pos int) ([]rline.Cand, int) {
	return []rline.Cand{{Text: "ect"}}, 3
}

// countingReader counts catalog calls per method.
type countingReader struct {
	candMockReader

	mu    sync.Mutex
	count map[string]int
}

func newCountingReader() *countingReader {
	return &countingReader{count: map[string]int{}}
}

func (r *countingReader) bump(op string) {
	r.mu.Lock()
	r.count[op]++
	r.mu.Unlock()
}

func (r *countingReader) calls(op string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count[op]
}

func (r *countingReader) Tables(f metadata.Filter) (*metadata.TableSet, error) {
	r.bump("tables")
	return r.candMockReader.Tables(f)
}

func (r *countingReader) Schemas(f metadata.Filter) (*metadata.SchemaSet, error) {
	r.bump("schemas")
	return r.candMockReader.Schemas(f)
}

func (r *countingReader) Columns(f metadata.Filter) (*metadata.ColumnSet, error) {
	r.bump("columns")
	return r.candMockReader.Columns(f)
}
