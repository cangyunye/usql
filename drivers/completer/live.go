package completer

import (
	"github.com/xo/usql/rline"
)

// NewLive wraps a completer with the typing-time entry points the UI layer
// expects (rline.LiveCompleter).
//
// All computation is synchronous: the lazy catalog cache inside the
// completer answers from memory whenever the needed level has loaded, and a
// miss returns whatever the keyword and meta tiers offer without blocking —
// the cache loads the missing level in a goroutine and fires its kick, which
// re-renders the pending completion with the real candidates. This keeps
// every keystroke responsive even when the metadata source is slow (e.g.
// OceanBase cold queries).
func NewLive(inner rline.Completer) rline.Completer {
	return &liveCompleter{inner: inner}
}

type liveCompleter struct {
	inner rline.Completer
}

var (
	_ rline.Completer     = &liveCompleter{}
	_ rline.LiveCompleter = &liveCompleter{}
	_ rline.Replacer      = &liveCompleter{}
)

// SetLiveKick satisfies rline.LiveCompleter: the UI layer injects its
// re-render hook, which the cache calls after each completed load.
func (l *liveCompleter) SetLiveKick(kick func()) {
	if ks, ok := l.inner.(interface{ SetKick(func()) }); ok {
		ks.SetKick(kick)
	}
}

// Invalidate forwards to the wrapped completer, dropping its cached
// metadata.
func (l *liveCompleter) Invalidate() {
	if inv, ok := l.inner.(interface{ Invalidate() }); ok {
		inv.Invalidate()
	}
}

// Do delegates the append-style path (TAB fallback, keywords, meta).
func (l *liveCompleter) Do(line []rune, pos int) ([]rline.Cand, int) {
	return l.inner.Do(line, pos)
}

// DoRepl forwards the wrapped completer's replace-style candidates, so TAB
// still replaces the word at the cursor with full object names.
func (l *liveCompleter) DoRepl(line []rune, pos int) ([]rline.Cand, int, bool) {
	if rp, ok := l.inner.(rline.Replacer); ok {
		return rp.DoRepl(line, pos)
	}
	return nil, 0, false
}

// DoLive is the typing-time path: replace-style candidates when the context
// engine has them, otherwise the append-style meta/keyword tiers. It never
// blocks on a query — a cache miss is answered from what is loaded and the
// kick re-renders when the rest lands.
func (l *liveCompleter) DoLive(line []rune, pos int) ([]rline.Cand, int, bool) {
	if cands, length, ok := l.DoRepl(line, pos); ok {
		return cands, length, true
	}
	cands, length := l.Do(line, pos)
	return cands, length, false
}
