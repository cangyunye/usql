package completer

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// countingCompleter serves one fixed option set and counts Do/DoRepl calls,
// so the memo tests can prove that typing within a word performs zero
// completer runs.
type countingCompleter struct {
	calls int32
}

func (s *countingCompleter) Do(line []rune, pos int) ([]rline.Cand, int) {
	atomic.AddInt32(&s.calls, 1)
	return rline.Cands("film_x"), 2
}

func (s *countingCompleter) DoRepl(line []rune, pos int) ([]rline.Cand, int, bool) {
	atomic.AddInt32(&s.calls, 1)
	// pretend to be the context path: full-word candidates for any word
	return []rline.Cand{
		{Text: "public.film", Kind: "table"},
		{Text: "public.film_view", Kind: "view"},
		{Text: "other.orders", Kind: "table"},
	}, pos, true
}

// optionsFor mirrors the real completer's optionsSource: the unfiltered
// option set for the word at the cursor.
func (s *countingCompleter) optionsFor(line []rune, pos int) ([]rline.Cand, string, bool) {
	ws := pos
	for ws > 0 && line[ws-1] != ' ' {
		ws--
	}
	return []rline.Cand{
		{Text: "public.film", Kind: "table"},
		{Text: "public.film_view", Kind: "view"},
		{Text: "other.orders", Kind: "table"},
	}, string(line[ws:pos]), true
}

func (s *countingCompleter) calls32() int32 { return atomic.LoadInt32(&s.calls) }

// waitMemo waits until the background memo fill lands.
func waitMemo(live rline.Completer) bool {
	lc := live.(*liveCompleter)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if lc.memoLen() >= 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestLiveMemoServesTypingWithoutCompleter(t *testing.T) {
	stub := &countingCompleter{}
	live := NewLive(stub).(rline.LiveCompleter)

	// first request at a word start: background, fills the memo
	line := []rune("SELECT * FROM f")
	if cands, _, _ := live.DoLive(line, 15); cands != nil {
		t.Fatalf("first DoLive = %v, want nil (computing)", cands)
	}
	if !waitMemo(live) {
		t.Fatal("memo was never filled")
	}
	base := stub.calls32()

	// typing within the same word: served by client-side re-filtering
	for _, word := range []string{"fi", "fil", "film"} {
		l := []rune("SELECT * FROM " + word)
		cands, length, replace := live.DoLive(l, len(l))
		if !replace {
			t.Fatalf("DoLive(%q) replace=false, want true", l)
		}
		if length != len(word) {
			t.Fatalf("DoLive(%q) length=%d, want %d", l, length, len(word))
		}
		if len(cands) == 0 || cands[0].Text != "public.film" || cands[0].Kind != "table" {
			t.Fatalf("DoLive(%q) = %v, want public.film first with kind table", l, cands)
		}
	}
	// backspace within the word: still the memo, zero completer runs
	for _, word := range []string{"fil", "fi"} {
		l := []rune("SELECT * FROM " + word)
		cands, _, _ := live.DoLive(l, len(l))
		if len(cands) == 0 || cands[0].Text != "public.film" {
			t.Fatalf("backspace DoLive(%q) = %v, want public.film first", l, cands)
		}
	}
	if got := stub.calls32(); got != base {
		t.Fatalf("completer ran %d -> %d times while typing one word, want no extra runs", base, got)
	}
}

func TestLiveMemoContextChangeRecomputes(t *testing.T) {
	stub := &countingCompleter{}
	live := NewLive(stub).(rline.LiveCompleter)

	line := []rune("SELECT * FROM f")
	if _, _, _ = live.DoLive(line, 15); candsNil(live) {
		// background accepted; wait for memo
		if !waitMemo(live) {
			t.Fatal("memo never filled")
		}
	}
	// a new word (after a space) is a different statement context: the memo
	// must not serve it
	l2 := []rune("SELECT * FROM film ")
	if _, _, replace := live.DoLive(l2, len(l2)); replace {
		t.Fatal("new word served from memo, want recomputation")
	}
}

func TestLiveTabUsesMemo(t *testing.T) {
	stub := &countingCompleter{}
	live := NewLive(stub).(rline.LiveCompleter)

	line := []rune("SELECT * FROM f")
	if _, _, _ = live.DoLive(line, 15); !waitMemo(live) {
		t.Fatal("memo never filled")
	}
	base := stub.calls32()

	l := []rune("SELECT * FROM fi")
	cands, length := live.Do(l, len(l))
	if len(cands) == 0 || cands[0].Text != "public.film" || length != 2 {
		t.Fatalf("TAB Do = %v,%d want public.film first,2", cands, length)
	}
	if got := stub.calls32(); got != base {
		t.Fatalf("TAB ran the completer (%d -> %d), want memo hit", base, got)
	}
}

func TestLiveMemoInvalidated(t *testing.T) {
	stub := &countingCompleter{}
	live := NewLive(stub).(rline.LiveCompleter)

	line := []rune("SELECT * FROM f")
	if _, _, _ = live.DoLive(line, 15); !waitMemo(live) {
		t.Fatal("memo never filled")
	}
	live.(interface{ Invalidate() }).Invalidate()
	if got := live.(*liveCompleter).memoLen(); got != -1 {
		t.Fatalf("memo holds %d options after Invalidate, want cleared", got)
	}
}

// genCompleter is an options source whose snapshot version changes, to test
// that memoized options are dropped when a newer snapshot lands.
type genCompleter struct {
	countingCompleter
	gen int32
}

func (g *genCompleter) snapState() (int64, bool) {
	return int64(atomic.LoadInt32(&g.gen)), true
}

func TestLiveMemoDroppedWhenSnapshotAdvances(t *testing.T) {
	stub := &genCompleter{}
	live := NewLive(stub).(rline.LiveCompleter)

	line := []rune("SELECT * FROM f")
	if _, _, _ = live.DoLive(line, 15); !waitMemo(live) {
		t.Fatal("memo never filled")
	}
	// the snapshot advanced: the memo must not serve stale options
	atomic.StoreInt32(&stub.gen, 1)
	if cands, _, ok := live.DoLive(line, 15); ok || cands != nil {
		t.Fatalf("stale memo served after snapshot advance: %v", cands)
	}
}

// TestLiveWindowToReadyTransition drives the real completer against a
// catalog whose load is held open: during the loading window completion
// answers empty (deferred), and once the snapshot lands the same keystroke
// recomputes into real candidates — no stale empty result survives.
func TestLiveWindowToReadyTransition(t *testing.T) {
	inner := &gatedReader{
		countingReader: countingReader{queries: map[string]int{}},
		gate:           make(chan struct{}),
		tables:         []metadata.Table{{Schema: "public", Name: "film", Type: "TABLE"}},
	}
	c := completer{reader: inner, logger: discardLogger(), schemaKind: "schema"}
	WithContextCompletion()(&c)
	live := NewLive(&c).(rline.LiveCompleter)

	line := []rune("SELECT * FROM fi")
	// the window: requests may answer nothing while the load is in flight
	// (the first one schedules the background compute)
	_, _, _ = live.DoLive(line, len(line))

	// release the snapshot and wait for the transition
	close(inner.gate)
	deadline := time.Now().Add(2 * time.Second)
	for {
		cands, _, _ := live.DoLive(line, len(line))
		if len(cands) > 0 {
			if cands[0].Text != "public.film" {
				t.Fatalf("candidates after load = %v, want public.film first", cands)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("candidates never recomputed after the snapshot landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// candsNil reports whether no background result has landed yet.
func candsNil(live rline.Completer) bool {
	return live.(*liveCompleter).memoLen() < 0
}
