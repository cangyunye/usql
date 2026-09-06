package completer

import (
	"strconv"
	"sync"

	"github.com/xo/usql/rline/readline"
)

// liveCacheSize caps how many (line, pos) results are remembered for
// typing-time display; typing produces one key per keystroke, so a small
// ring is plenty.
const liveCacheSize = 64

// NewLive wraps an AutoCompleter with a typing-time fast path. Do (the TAB
// path) delegates synchronously. DoLive returns a remembered result
// immediately — typically empty while a background query runs — and
// schedules the query off the input loop; when it lands, kick re-renders the
// candidate menu. This keeps every keystroke responsive even when the
// metadata source is slow (e.g. OceanBase cold queries).
func NewLive(inner readline.AutoCompleter) readline.AutoCompleter {
	return &liveCompleter{inner: inner}
}

type liveCompleter struct {
	inner readline.AutoCompleter

	mu        sync.Mutex
	kick      func()
	computing bool
	keys      []string
	cache     map[string]liveResult
}

type liveResult struct {
	cands  [][]rune
	length int
}

var _ readline.LiveAutoCompleter = &liveCompleter{}

// SetLiveKick satisfies readline.LiveAutoCompleter; the readline layer
// injects its re-render hook once the instance is wired up.
func (l *liveCompleter) SetLiveKick(kick func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kick = kick
}

// Invalidate drops remembered typing-time results and forwards to the
// wrapped completer, dropping its cached metadata queries too.
func (l *liveCompleter) Invalidate() {
	l.mu.Lock()
	l.keys, l.cache = nil, nil
	inner, _ := l.inner.(interface{ Invalidate() })
	l.mu.Unlock()
	if inner != nil {
		inner.Invalidate()
	}
}

// Do is the synchronous completion path (TAB), delegated unchanged.
func (l *liveCompleter) Do(line []rune, pos int) ([][]rune, int) {
	return l.inner.Do(line, pos)
}

// DoLive is the typing-time path: serve from memory when possible and never
// block the input loop on a query.
func (l *liveCompleter) DoLive(line []rune, pos int) ([][]rune, int) {
	key := liveKey(line, pos)
	l.mu.Lock()
	if res, ok := l.cache[key]; ok {
		l.mu.Unlock()
		return res.cands, res.length
	}
	if l.computing {
		// a query is already in flight; its kick will re-render, and the
		// next keystroke re-requests whatever is still missing
		l.mu.Unlock()
		return nil, 0
	}
	l.computing = true
	l.mu.Unlock()

	lineCopy := append([]rune(nil), line...)
	go func() {
		cands, length := l.inner.Do(lineCopy, pos)
		l.mu.Lock()
		if l.cache == nil {
			l.cache = make(map[string]liveResult, liveCacheSize)
		}
		l.cache[key] = liveResult{cands, length}
		l.keys = append(l.keys, key)
		for len(l.keys) > liveCacheSize {
			delete(l.cache, l.keys[0])
			l.keys = l.keys[1:]
		}
		kick := l.kick
		l.computing = false
		l.mu.Unlock()
		if kick != nil {
			kick()
		}
	}()
	return nil, 0
}

// liveKey identifies a completion request: the line up to the cursor plus
// the cursor position.
func liveKey(line []rune, pos int) string {
	if pos > len(line) {
		pos = len(line)
	}
	return string(line[:pos]) + "\x00" + strconv.Itoa(pos)
}

// liveCacheLen reports the number of remembered results; for tests.
func (l *liveCompleter) liveCacheLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}
