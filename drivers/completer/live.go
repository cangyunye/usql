package completer

import (
	"strconv"
	"strings"
	"sync"

	"github.com/xo/usql/rline"
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
//
// On top of the per-keystroke cache, the wrapper memoizes the context
// path's UNFILTERED option set once per statement context (see
// optionsSource): while the user keeps typing within one word, every
// keystroke is served by re-filtering that set in-process — zero completer
// work, zero queries, including on backspaces that merely shorten the word.
func NewLive(inner rline.Completer) rline.Completer {
	return &liveCompleter{inner: inner}
}

type liveCompleter struct {
	inner rline.Completer

	mu        sync.Mutex
	kick      func()
	computing bool
	keys      []string
	cache     map[string]liveResult

	memoKey  string
	memoOpts []rline.Cand
	memoGen  int64
}

type liveResult struct {
	cands   []rline.Cand
	length  int
	replace bool
	gen     int64 // catalog snapshot version the result was computed at
}

var _ rline.LiveCompleter = &liveCompleter{}

// SetLiveKick satisfies rline.LiveCompleter; the UI layer injects its
// re-render hook once the instance is wired up.
func (l *liveCompleter) SetLiveKick(kick func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kick = kick
}

// Invalidate drops remembered typing-time results, the statement-context
// memo, and forwards to the wrapped completer, dropping its cached metadata
// queries too.
func (l *liveCompleter) Invalidate() {
	l.mu.Lock()
	l.keys, l.cache = nil, nil
	l.memoKey, l.memoOpts = "", nil
	inner, _ := l.inner.(interface{ Invalidate() })
	l.mu.Unlock()
	if inner != nil {
		inner.Invalidate()
	}
}

// memoKey identifies the statement context of a completion request: the line
// up to the word at the cursor, plus the word's dotted qualifier (the
// qualifier selects the candidate tier — schemas vs a namespace's objects).
// A dotless word has no qualifier, so all its prefixes share one key.
func memoKey(line []rune, pos int) (key, word string) {
	if pos > len(line) {
		pos = len(line)
	}
	ws := pos
	for ws > 0 && !strings.ContainsRune(WORD_BREAKS, line[ws-1]) {
		ws--
	}
	word = string(line[ws:pos])
	qual := ""
	if d := strings.LastIndexByte(word, '.'); d >= 0 {
		qual = word[:d+1]
	}
	return string(line[:ws]) + "\x00" + qual, word
}

// snapGen reports the catalog snapshot version; 0 without a snapshot.
func (l *liveCompleter) snapGen() int64 {
	if ss, ok := l.inner.(interface{ snapState() (int64, bool) }); ok {
		g, _ := ss.snapState()
		return g
	}
	return 0
}

// memoLookup filters the memoized option set for the word at the cursor.
func (l *liveCompleter) memoLookup(line []rune, pos int) ([]rline.Cand, int, bool) {
	key, word := memoKey(line, pos)
	l.mu.Lock()
	defer l.mu.Unlock()
	cands, length, replace, ok := l.memoLookupLocked(line, pos, key, word, l.snapGen())
	if !ok {
		return nil, 0, false
	}
	return cands, length, replace
}

// memoLookupLocked is memoLookup for callers already holding l.mu (or with
// the key, word and snapshot version at hand).
func (l *liveCompleter) memoLookupLocked(line []rune, pos int, key, word string, gen int64) ([]rline.Cand, int, bool, bool) {
	if l.memoKey != key || l.memoOpts == nil {
		return nil, 0, false, false
	}
	if l.memoGen != gen {
		// a newer catalog snapshot landed: the options predate it
		l.memoKey, l.memoOpts = "", nil
		return nil, 0, false, false
	}
	cands := completePrefixFull(word, l.memoOpts)
	return cands, len([]rune(word)), true, true
}

// memoFill runs the wrapped completer's optionsFor and remembers the option
// set, reporting whether the request can be answered from it.
func (l *liveCompleter) memoFill(line []rune, pos int) ([]rline.Cand, int, bool) {
	src, ok := l.inner.(optionsSource)
	if !ok {
		return nil, 0, false
	}
	opts, word, ok := src.optionsFor(line, pos)
	if !ok || len(opts) == 0 {
		return nil, 0, false
	}
	key, memoWord := memoKey(line, pos)
	gen := l.snapGen()
	l.mu.Lock()
	if memoWord == word {
		l.memoKey, l.memoOpts, l.memoGen = key, opts, gen
	}
	l.mu.Unlock()
	cands := completePrefixFull(word, opts)
	return cands, len([]rune(word)), true
}

// Do is the synchronous completion path (TAB): the memo serves it when the
// context is known, otherwise it delegates unchanged.
func (l *liveCompleter) Do(line []rune, pos int) ([]rline.Cand, int) {
	if cands, length, ok := l.memoLookup(line, pos); ok {
		return cands, length
	}
	return l.inner.Do(line, pos)
}

// DoRepl forwards the wrapped completer's replace-style candidates, so TAB
// through the live wrapper still replaces the word at the cursor.
func (l *liveCompleter) DoRepl(line []rune, pos int) ([]rline.Cand, int, bool) {
	if rp, ok := l.inner.(rline.Replacer); ok {
		return rp.DoRepl(line, pos)
	}
	return nil, 0, false
}

// DoLive is the typing-time path: serve from memory when possible and never
// block the input loop on a query. The replace result reports whether the
// candidates replace the word at the cursor (full names) or append after it
// (suffixes).
func (l *liveCompleter) DoLive(line []rune, pos int) ([]rline.Cand, int, bool) {
	key := liveKey(line, pos)
	gen := l.snapGen()
	l.mu.Lock()
	if res, ok := l.cache[key]; ok && res.gen == gen {
		l.mu.Unlock()
		return res.cands, res.length, res.replace
	}
	memoKeyStr, memoWord := memoKey(line, pos)
	if cands, length, replace, ok := l.memoLookupLocked(line, pos, memoKeyStr, memoWord, gen); ok {
		l.mu.Unlock()
		return cands, length, replace
	}
	if l.computing {
		// a query is already in flight; its kick will re-render, and the
		// next keystroke re-requests whatever is still missing
		l.mu.Unlock()
		return nil, 0, false
	}
	l.computing = true
	l.mu.Unlock()

	lineCopy := append([]rune(nil), line...)
	go func() {
		cands, length, replace := l.computeOffLoop(lineCopy, pos)
		l.mu.Lock()
		if l.cache == nil {
			l.cache = make(map[string]liveResult, liveCacheSize)
		}
		l.cache[key] = liveResult{cands, length, replace, gen}
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
	return nil, 0, false
}

// computeOffLoop answers a background request: through the option memo when
// the context path can supply one (the result doubles as the memo fill),
// otherwise through the wrapped completer's replace-aware path.
func (l *liveCompleter) computeOffLoop(line []rune, pos int) ([]rline.Cand, int, bool) {
	if cands, length, replace := l.memoFill(line, pos); replace {
		return cands, length, true
	}
	return l.compute(line, pos)
}

// compute runs the wrapped completer's replace-aware path when available,
// falling back to plain Do.
func (l *liveCompleter) compute(line []rune, pos int) ([]rline.Cand, int, bool) {
	if rp, ok := l.inner.(rline.Replacer); ok {
		if cands, length, replace := rp.DoRepl(line, pos); replace {
			return cands, length, true
		}
	}
	cands, length := l.inner.Do(line, pos)
	return cands, length, false
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

// memoLen reports whether the statement-context memo is populated; for tests.
func (l *liveCompleter) memoLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.memoOpts == nil {
		return -1
	}
	return len(l.memoOpts)
}
