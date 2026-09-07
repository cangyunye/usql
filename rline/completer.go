package rline

// Completer is the auto-completion interface, decoupled from any specific
// readline implementation. Do is passed the whole line and the cursor
// position; it returns the candidates and how many runes before the cursor
// they share with the line:
//
//	Do("g", 1)  => ["o", "it", "it-shell", "rep"], 1
//	Do("gi", 2) => ["t", "t-shell"], 2
type Completer interface {
	// Do returns completions for line at the cursor position pos.
	Do(line []rune, pos int) (newLine [][]rune, length int)
}

// LiveCompleter is an optional Completer extension for typing-time
// completions that never block the input loop. DoLive may return no
// candidates while a background query runs, and must call the kick func
// once results are available so the UI can re-render its candidate menu.
type LiveCompleter interface {
	Completer
	// DoLive serves typing-time completions from memory when possible.
	DoLive(line []rune, pos int) (newLine [][]rune, length int, replace bool)
	// SetLiveKick registers the re-render hook invoked when a background
	// result lands.
	SetLiveKick(kick func())
}

// Replacer is an optional Completer extension for candidates that REPLACE
// the word at the cursor (e.g. fully qualified schema.table names) instead
// of appending a suffix after it. When DoRepl returns ok, the last length
// runes before the cursor are replaced by the chosen candidate, and menus
// list the candidates in full.
type Replacer interface {
	// DoRepl returns replace-style candidates for line at pos.
	DoRepl(line []rune, pos int) (newLine [][]rune, length int, ok bool)
}

// CompleterSwapper is an optional IO extension that lets callers (e.g. the
// \conns manager) temporarily replace the active completer with one fitted
// to their own prompts, restoring the previous one afterwards. The IO
// implementations swap out only the completer, never the engine.
type CompleterSwapper interface {
	// SwapCompleter installs c and returns a func restoring the previous
	// completer.
	SwapCompleter(c Completer) (restore func())
}

// completerAdapter forwards a Completer to the readline.AutoCompleter
// interface, including the optional LiveCompleter and Replacer extensions.
// The optional methods degrade to the plain Do path when the wrapped
// Completer does not implement them, matching the readline layer's own
// fallthrough.
type completerAdapter struct {
	c Completer
}

// Do satisfies readline.AutoCompleter.
func (a completerAdapter) Do(line []rune, pos int) ([][]rune, int) {
	return a.c.Do(line, pos)
}

// DoLive satisfies readline.LiveAutoCompleter. When the wrapped Completer
// does not provide the live fast path, it degrades to the synchronous Do
// (append-style), so plain completers stay usable under the adapter: the
// readline engine takes DoLive's result as final and never falls through to
// Do on its own.
func (a completerAdapter) DoLive(line []rune, pos int) ([][]rune, int, bool) {
	if lc, ok := a.c.(LiveCompleter); ok {
		return lc.DoLive(line, pos)
	}
	newLines, length := a.c.Do(line, pos)
	return newLines, length, false
}

// SetLiveKick satisfies readline.LiveAutoCompleter.
func (a completerAdapter) SetLiveKick(kick func()) {
	if lc, ok := a.c.(LiveCompleter); ok {
		lc.SetLiveKick(kick)
	}
}

// DoRepl satisfies readline.Replacer.
func (a completerAdapter) DoRepl(line []rune, pos int) ([][]rune, int, bool) {
	if rp, ok := a.c.(Replacer); ok {
		return rp.DoRepl(line, pos)
	}
	return nil, 0, false
}
