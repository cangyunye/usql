package completer

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xo/usql/rline/readline"
)

// stubCompleter delays and counts synchronous Do calls.
type stubCompleter struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (s *stubCompleter) Do(line []rune, pos int) ([][]rune, int) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	time.Sleep(s.delay)
	return [][]rune{[]rune("ilm")}, 2
}

func (s *stubCompleter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestLiveTabPathIsSynchronous(t *testing.T) {
	stub := &stubCompleter{}
	live := NewLive(stub).(readline.LiveAutoCompleter)

	cands, length := live.Do([]rune("SELECT * FROM fi"), 16)
	if len(cands) != 1 || string(cands[0]) != "ilm" || length != 2 {
		t.Errorf("Do = %q,%d want [ilm],2", cands, length)
	}
	if n := stub.callCount(); n != 1 {
		t.Errorf("inner called %d times, want 1", n)
	}
}

func TestLiveDoLiveServesFromCache(t *testing.T) {
	stub := &stubCompleter{}
	live := NewLive(stub).(readline.LiveAutoCompleter)

	line := []rune("SELECT * FROM fi")
	// first request: background, returns nothing yet
	if cands, _ := live.DoLive(line, 16); cands != nil {
		t.Errorf("first DoLive = %q, want nil (computing)", cands)
	}
	// wait for the background query + kick bookkeeping
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cands, length := live.DoLive(line, 16); cands != nil {
			if string(cands[0]) != "ilm" || length != 2 {
				t.Errorf("cached DoLive = %q,%d want [ilm],2", cands, length)
			}
			if n := stub.callCount(); n != 1 {
				t.Errorf("inner called %d times, want 1", n)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cached result never appeared")
}

func TestLiveKickFiresOnce(t *testing.T) {
	stub := &stubCompleter{delay: 20 * time.Millisecond}
	live := NewLive(stub).(readline.LiveAutoCompleter)

	kicks := make(chan struct{}, 1)
	live.SetLiveKick(func() { kicks <- struct{}{} })

	live.DoLive([]rune("SELECT * FROM f"), 15)
	select {
	case <-kicks:
	case <-time.After(time.Second):
		t.Fatal("kick never fired after background completion")
	}
}

func TestLiveSkipsWhileComputing(t *testing.T) {
	stub := &stubCompleter{delay: 50 * time.Millisecond}
	live := NewLive(stub).(readline.LiveAutoCompleter)

	live.DoLive([]rune("SELECT * FROM f"), 15)
	// different keystroke while the first query is in flight: declined
	if cands, _ := live.DoLive([]rune("SELECT * FROM fi"), 16); cands != nil {
		t.Errorf("DoLive while computing = %q, want nil", cands)
	}
	time.Sleep(80 * time.Millisecond)
	// after the in-flight query landed, the new key is served on its own
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cands, _ := live.DoLive([]rune("SELECT * FROM fi"), 16); cands != nil {
			if n := stub.callCount(); n != 2 {
				t.Errorf("inner called %d times, want 2", n)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("second result never appeared; inner calls = %d", stub.callCount())
}

func TestLiveCacheEvicts(t *testing.T) {
	stub := &stubCompleter{}
	live := NewLive(stub).(readline.LiveAutoCompleter)

	// fill well past the cache size with distinct lines
	for i := 0; i < liveCacheSize+10; i++ {
		live.DoLive([]rune(fmt.Sprintf("SELECT * FROM x%d", i)), 17)
	}
	// wait for all background queries to finish
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stub.callCount() == liveCacheSize+10 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if n := live.(*liveCompleter).liveCacheLen(); n > liveCacheSize {
		t.Errorf("cache holds %d keys, want at most %d", n, liveCacheSize)
	}
}
