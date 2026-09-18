package rline

// Cand is one completion candidate: the text to insert (a suffix to append
// after the word at the cursor, or a full word that replaces it — see
// Replacer) plus a Kind describing what the candidate is, shown as a dim
// badge in the TUI candidate menu.
type Cand struct {
	Text string
	Kind string // display class: "table", "view", "schema", "user", ... ("" for keywords)
}

// Cands wraps plain candidate strings as untyped candidates.
func Cands(texts ...string) []Cand {
	if texts == nil {
		return nil
	}
	out := make([]Cand, 0, len(texts))
	for _, t := range texts {
		out = append(out, Cand{Text: t})
	}
	return out
}

// Texts strips the candidates back to their text, for consumers that do not
// display kinds (the plain readline engine, tests).
func Texts(cands []Cand) [][]rune {
	if cands == nil {
		return nil
	}
	out := make([][]rune, 0, len(cands))
	for _, c := range cands {
		out = append(out, []rune(c.Text))
	}
	return out
}

// Completer is the auto-completion interface, decoupled from any specific
// readline implementation. Do is passed the whole line and the cursor
// position; it returns the candidates and how many runes before the cursor
// they share with the line:
//
//	Do("g", 1)  => ["o", "it", "it-shell", "rep"], 1
//	Do("gi", 2) => ["t", "t-shell"], 2
type Completer interface {
	// Do returns completions for line at the cursor position pos.
	Do(line []rune, pos int) (newLine []Cand, length int)
}

// LiveCompleter is an optional Completer extension for typing-time
// completions that never block the input loop. DoLive may return no
// candidates while a background query runs, and must call the kick func
// once results are available so the UI can re-render its candidate menu.
type LiveCompleter interface {
	Completer
	// DoLive serves typing-time completions from memory when possible.
	DoLive(line []rune, pos int) (newLine []Cand, length int, replace bool)
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
	DoRepl(line []rune, pos int) (newLine []Cand, length int, ok bool)
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
// fallthrough. Candidates cross the boundary as plain text: the classic
// readline engine has no kind concept.
type completerAdapter struct {
	c Completer
}

// Do satisfies readline.AutoCompleter.
func (a completerAdapter) Do(line []rune, pos int) ([][]rune, int) {
	newLine, length := a.c.Do(line, pos)
	return Texts(newLine), length
}

// DoLive satisfies readline.LiveAutoCompleter. When the wrapped Completer
// does not provide the live fast path, it degrades to the synchronous Do
// (append-style), so plain completers stay usable under the adapter: the
// readline engine takes DoLive's result as final and never falls through to
// Do on its own.
func (a completerAdapter) DoLive(line []rune, pos int) ([][]rune, int, bool) {
	if lc, ok := a.c.(LiveCompleter); ok {
		newLine, length, replace := lc.DoLive(line, pos)
		return Texts(newLine), length, replace
	}
	newLine, length := a.c.Do(line, pos)
	return Texts(newLine), length, false
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
		newLine, length, ok := rp.DoRepl(line, pos)
		return Texts(newLine), length, ok
	}
	return nil, 0, false
}
