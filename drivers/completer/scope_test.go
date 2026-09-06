package completer

import (
	"testing"
	"time"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline/readline"
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

func TestInvalidateDropsReaderCache(t *testing.T) {
	inner := newCountingReader()
	c := completer{reader: inner, logger: discardLogger()}
	WithContextCompletion()(&c)
	if c.cache == nil {
		t.Fatal("WithContextCompletion did not install a cache")
	}

	tr := c.reader.(metadata.TableReader)
	for i := 0; i < 2; i++ {
		tr.Tables(metadata.Filter{OnlyVisible: true})
	}
	if n := inner.calls("tables"); n != 1 {
		t.Fatalf("inner queried %d times before invalidate, want 1", n)
	}

	c.Invalidate()
	tr.Tables(metadata.Filter{OnlyVisible: true})
	if n := inner.calls("tables"); n != 2 {
		t.Errorf("inner queried %d times after invalidate, want 2", n)
	}
}

func TestLiveInvalidateDropsTypedResults(t *testing.T) {
	stub := &stubCompleter{}
	live := NewLive(stub).(readline.LiveAutoCompleter)
	inv, ok := live.(interface{ Invalidate() })
	if !ok {
		t.Fatal("NewLive result does not implement Invalidate")
	}
	line := []rune("SELECT * FROM fi")

	// fill and wait for the background result
	live.DoLive(line, 16)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cands, _, _ := live.DoLive(line, 16); cands != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	before := stub.callCount()

	inv.Invalidate()
	if cands, _, _ := live.DoLive(line, 16); cands != nil {
		t.Errorf("DoLive right after Invalidate = %q, want nil (recomputing)", cands)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cands, _, _ := live.DoLive(line, 16); cands != nil {
			if stub.callCount() != before+1 {
				t.Errorf("inner called %d -> %d, want +1 after invalidate", before, stub.callCount())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("result never recomputed after invalidate")
}
