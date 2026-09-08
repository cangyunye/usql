package rline

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEditorBasics(t *testing.T) {
	var e editor
	for _, r := range "SELECT" {
		e.insert(r)
	}
	if e.String() != "SELECT|" || !e.atEnd() {
		t.Fatalf("after insert: %q atEnd=%v", e.String(), e.atEnd())
	}
	e.moveStart()
	e.moveRight()
	if got := e.String(); got != "S|ELECT" {
		t.Fatalf("after moveRight: %q", got)
	}
	e.backspace()
	if got := e.String(); got != "|ELECT" {
		t.Fatalf("after backspace: %q", got)
	}
	e.moveEnd()
	e.insertRunes([]rune(" * FROM"))
	if got := string(e.line()); got != "ELECT * FROM" {
		t.Fatalf("after insertRunes: %q", got)
	}
}

func TestEditorKillYank(t *testing.T) {
	var e editor
	e.reset([]rune("one two three"))
	e.moveEnd()
	e.killToEnd() // cursor at end: nothing to kill
	if got := string(e.line()); got != "one two three" {
		t.Fatalf("killToEnd at end: %q", got)
	}
	e.killPrevWord() // kills " three"
	if got := e.String(); got != "one two |" {
		t.Fatalf("killPrevWord: %q", got)
	}
	e.killToStart() // kills "one two"
	if !e.empty() {
		t.Fatalf("killToStart: %q", e.String())
	}
	// consecutive kills accumulate in cut order
	if !e.yank() {
		t.Fatal("yank failed")
	}
	if got := string(e.line()); got != "threeone two " {
		t.Fatalf("yank: %q", got)
	}
	// word kill: Ctrl-W removes the word before the cursor, including the
	// space that separated it
	e.reset([]rune("alpha beta"))
	e.moveWordLeft()
	e.killPrevWord()
	if got := e.String(); got != "|beta" {
		t.Fatalf("killPrevWord: %q", got)
	}
}

func TestEditorReplaceBefore(t *testing.T) {
	var e editor
	e.reset([]rune("select * from film"))
	e.moveEnd()
	e.deleteRunes(2) // erase "lm"
	if got := e.String(); got != "select * from fi|" {
		t.Fatalf("after deleteRunes: %q", got)
	}
	e.replaceBefore(0, []rune("lm"))
	if got := string(e.line()); got != "select * from film" {
		t.Fatalf("after replaceBefore: %q", got)
	}
}

func TestTUIHistoryPrevNextDraft(t *testing.T) {
	h := &tuiHistory{lines: []string{"select 1", "select 2"}}
	pos := 2 // at the draft
	pos, line, ok := h.prev(pos, "sel")
	if !ok || line != "select 2" {
		t.Fatalf("prev: %q %v", line, ok)
	}
	pos, line, ok = h.next(pos, "sel")
	if !ok || line != "sel" {
		t.Fatalf("next to draft: %q %v", line, ok)
	}
	pos, line, ok = h.next(pos, "sel")
	if ok {
		t.Fatal("next past draft should not move")
	}
}

func TestTUIHistorySearchAndSuggest(t *testing.T) {
	h := &tuiHistory{lines: []string{"SELECT * FROM a", "select 1", "SELECT * FROM b"}}
	if i, line, ok := h.search("select * from", 3); !ok || line != "SELECT * FROM b" || i != 2 {
		t.Fatalf("search: %d %q %v", i, line, ok)
	}
	if i, line, ok := h.search("select * from", 2); !ok || line != "SELECT * FROM a" || i != 0 {
		t.Fatalf("search again: %d %q %v", i, line, ok)
	}
	if got := h.suggest([]rune("sel")); string(got) != "ect 1" {
		t.Fatalf("suggest: %q", got)
	}
	if got := h.suggest([]rune("no match")); got != nil {
		t.Fatalf("suggest no match: %q", got)
	}
}

func TestTUIHistoryFilePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")
	h := loadTUIHistory(path)
	for _, s := range []string{"select a", "select b", ""} {
		if err := h.Save(s); err != nil {
			t.Fatalf("Save(%q): %v", s, err)
		}
	}
	// empty lines are not persisted
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "select a\nselect b\n" {
		t.Fatalf("file = %q", string(b))
	}
	// reload caps past historyMaxLines
	h2 := loadTUIHistory(path)
	if len(h2.lines) != 2 || h2.lines[1] != "select b" {
		t.Fatalf("reloaded: %q", h2.lines)
	}
}

// keyTyped builds a printable-key message.
func keyTyped(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyName(name string) tea.KeyMsg {
	names := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "ctrl+c": tea.KeyCtrlC, "ctrl+d": tea.KeyCtrlD,
		"tab": tea.KeyTab, "esc": tea.KeyEscape, "up": tea.KeyUp,
		"down": tea.KeyDown, "right": tea.KeyRight, "ctrl+r": tea.KeyCtrlR,
		"backspace": tea.KeyBackspace,
	}
	if kt, ok := names[name]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// typeLine drives the model with a string and returns it.
func typeLine(m *lineModel, s string) *lineModel {
	for _, r := range s {
		mod, _ := m.Update(keyTyped(string(r)))
		m = mod.(*lineModel)
		// bubbletea renders after every update; the synchronous highlight
		// compute lives in View
		m.View()
	}
	return m
}

func TestLineModelGhostAccept(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.hist = &tuiHistory{lines: []string{"select * from align"}}
	tr.Prompt("usql> ")
	m := newLineModel(tr, "usql> ", -1)
	m = typeLine(m, "sel")
	if len(m.ghost) == 0 {
		t.Fatal("expected a ghost suggestion after typing a history prefix")
	}
	// right arrow accepts the whole suggestion
	mod, _ := m.Update(keyName("right"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select * from align" {
		t.Fatalf("after ghost accept: %q", got)
	}
}

func TestLineModelCtrlLClearScreen(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.hist = &tuiHistory{lines: []string{"select * from align"}}
	tr.Prompt("usql> ")
	m := newLineModel(tr, "usql> ", -1)
	m = typeLine(m, "sel")
	if len(m.ghost) == 0 {
		t.Fatal("expected a ghost suggestion")
	}
	// ctrl+l clears the screen and repaints the line in place: buffer and
	// ghost survive, the line is not submitted, and a repaint is requested
	mod, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = mod.(*lineModel)
	if cmd == nil {
		t.Fatal("ctrl+l should request a repaint")
	}
	if got := string(m.ed.buf); got != "sel" {
		t.Fatalf("ctrl+l changed the buffer: %q", got)
	}
	if len(m.ghost) == 0 {
		t.Fatal("ctrl+l should keep the ghost (repaint, not dismissal)")
	}
	if m.done {
		t.Fatal("ctrl+l must not submit the line")
	}
	// editing continues afterwards and the line still submits normally
	m = typeLine(m, "ect 1")
	mod, _ = m.Update(keyName("enter"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); !m.done || got != "select 1" {
		t.Fatalf("after ctrl+l: done=%v buf=%q", m.done, got)
	}
}

func TestLineModelInterruptAndEOF(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("ctrl+c"))
	m = mod.(*lineModel)
	if !m.done || !m.interrupted {
		t.Fatal("ctrl+c must finalize with interrupt")
	}
	m2 := newLineModel(tr, "> ", -1)
	mod, _ = m2.Update(keyName("ctrl+d"))
	m2 = mod.(*lineModel)
	if !m2.done || m2.interrupted {
		t.Fatal("ctrl+d on empty line must finalize as EOF")
	}
}

// stubCompleter returns fixed suffix candidates.
type stubCompleter struct {
	cands []string
}

func (s *stubCompleter) Do(line []rune, pos int) ([][]rune, int) {
	out := make([][]rune, 0, len(s.cands))
	for _, c := range s.cands {
		out = append(out, []rune(c))
	}
	return out, len(line)
}

func TestLineModelMenuTabAccept(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"ect", "ect 1"}})
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "sel")
	// Tab opens the vertical menu
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	if m.menu == nil || len(m.menu) != 2 {
		t.Fatalf("tab did not open menu: %+v", m.menu)
	}
	// down selects, enter accepts the candidate but not the line
	mod, _ = m.Update(keyName("down"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("enter"))
	m = mod.(*lineModel)
	if m.done {
		t.Fatal("enter must accept the candidate, not finalize the line")
	}
	if got := string(m.ed.buf); got != "select 1" {
		t.Fatalf("after menu accept: %q", got)
	}
	if m.menu != nil {
		t.Fatal("menu must close after accepting")
	}
}

func TestLineModelMenuAltDigit(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"ect", "ect 1"}})
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1"), Alt: true})
	m = mod.(*lineModel)
	if m.done {
		t.Fatal("alt+digit must accept the candidate, not finalize")
	}
	if got := string(m.ed.buf); got != "select" {
		t.Fatalf("alt+1 accept: %q", got)
	}
}

func TestLineModelMenuFilterWhileTyping(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"ect", "ection"}})
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "sel")
	mod, _ := m.Update(keyName("tab"))
	m = mod.(*lineModel)
	if m.menu == nil {
		t.Fatal("menu did not open")
	}
	// typing keeps the menu open and refilters
	mod, _ = m.Update(keyTyped("i"))
	m = mod.(*lineModel)
	if m.menu == nil {
		t.Fatal("menu closed while typing")
	}
	// esc closes it
	mod, _ = m.Update(keyName("esc"))
	m = mod.(*lineModel)
	if m.menu != nil {
		t.Fatal("esc did not close the menu")
	}
}

func TestLineModelHistoryNav(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.hist = &tuiHistory{lines: []string{"select 1", "select 2"}}
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	mod, _ := m.Update(keyName("up"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 2" {
		t.Fatalf("up: %q", got)
	}
	mod, _ = m.Update(keyName("up"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 1" {
		t.Fatalf("up again: %q", got)
	}
	// editing a history entry does not change the draft: down walks to the
	// next entry, then past the newest entry restores the original draft
	mod, _ = m.Update(keyTyped("x"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("down"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 2" {
		t.Fatalf("down: %q", got)
	}
	mod, _ = m.Update(keyName("down"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "" {
		t.Fatalf("down to draft: %q", got)
	}
}

func TestLineModelIncrementalSearch(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.hist = &tuiHistory{lines: []string{"SELECT 1 FROM a", "select 2"}}
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	mod, _ := m.Update(keyName("ctrl+r"))
	m = mod.(*lineModel)
	if !m.searching {
		t.Fatal("ctrl+r did not enter search mode")
	}
	m = typeLine(m, "select 1")
	if got := string(m.ed.buf); got != "SELECT 1 FROM a" {
		t.Fatalf("search match: %q", got)
	}
	// enter accepts the match without executing
	mod, _ = m.Update(keyName("enter"))
	m = mod.(*lineModel)
	if m.searching || m.done {
		t.Fatalf("enter in search: searching=%v done=%v", m.searching, m.done)
	}
	if got := string(m.ed.buf); got != "SELECT 1 FROM a" {
		t.Fatalf("after search accept: %q", got)
	}
}

func TestTUIHistoryFwdSearch(t *testing.T) {
	h := &tuiHistory{lines: []string{"select 1", "update x", "select 2", "select 3"}}
	// forward from an older match finds the next newer entry
	if i, line, ok := h.fwdSearch("select", 1); !ok || i != 2 || line != "select 2" {
		t.Fatalf("fwdSearch: %d %q %v", i, line, ok)
	}
	// forward from the draft finds nothing
	if _, _, ok := h.fwdSearch("select", 4); ok {
		t.Fatal("fwdSearch from draft must not match")
	}
	// forward before the first entry starts at the first entry
	if i, _, ok := h.fwdSearch("select", -2); !ok || i != 0 {
		t.Fatalf("fwdSearch clamped: %d %v", i, ok)
	}
}

func TestLineModelSearchBidirectional(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.hist = &tuiHistory{lines: []string{"select 1 from a", "update b", "select 2 from c"}}
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	// ctrl+r lands on the newest match
	mod, _ := m.Update(keyName("ctrl+r"))
	m = mod.(*lineModel)
	m = typeLine(m, "select")
	if got := string(m.ed.buf); got != "select 2 from c" {
		t.Fatalf("reverse match: %q", got)
	}
	// repeated ctrl+r steps to the older match
	mod, _ = m.Update(keyName("ctrl+r"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 1 from a" {
		t.Fatalf("reverse repeat: %q", got)
	}
	// ctrl+s steps back to the newer match
	mod, _ = m.Update(keyName("ctrl+s"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "select 2 from c" {
		t.Fatalf("forward: %q", got)
	}
	if m.searchFail {
		t.Fatal("forward match must clear searchFail")
	}
	// ctrl+s past the newest match marks the search failed
	mod, _ = m.Update(keyName("ctrl+s"))
	m = mod.(*lineModel)
	if !m.searchFail {
		t.Fatal("exhausted forward search must flag failure")
	}
	// ctrl+g restores the buffer and position from before the search
	mod, _ = m.Update(keyName("ctrl+g"))
	m = mod.(*lineModel)
	if m.searching || m.hasHist || string(m.ed.buf) != "" {
		t.Fatalf("ctrl+g cancel: searching=%v hasHist=%v buf=%q", m.searching, m.hasHist, string(m.ed.buf))
	}
	// a failed query shows the failed marker
	mod, _ = m.Update(keyName("ctrl+r"))
	m = mod.(*lineModel)
	m = typeLine(m, "zzz")
	if v := m.View(); !strings.Contains(v, "(failed reverse-i-search)") {
		t.Fatalf("failed marker missing: %q", v)
	}
}

func TestEditorTransposeAndYankPop(t *testing.T) {
	var e editor
	e.reset([]rune("abc"))
	e.moveEnd()
	e.transpose()
	if got := e.String(); got != "acb|" {
		t.Fatalf("transpose at end: %q", got)
	}
	e.moveLeft()
	e.transpose()
	if got := e.String(); got != "abc|" {
		t.Fatalf("transpose mid-line: %q", got)
	}
	// alt-t: swap the two words before the cursor
	e.reset([]rune("foo bar baz"))
	e.moveEnd()
	e.transposeWords()
	if got := string(e.line()); got != "foo baz bar" || !e.atEnd() {
		t.Fatalf("transposeWords at end: %q idx=%d", got, e.idx)
	}
	e.reset([]rune("foo bar baz qux"))
	e.idx = 11 // cursor after "baz"
	e.transposeWords()
	if got := e.String(); got != "foo baz bar| qux" {
		t.Fatalf("transposeWords mid-line: %q", got)
	}
	// ctrl-y pastes the front entry; alt-y cycles to the older one
	e.reset([]rune(""))
	e.killRing = [][]rune{[]rune("older"), []rune("newer")}
	e.yank()
	if got := e.String(); got != "newer|" {
		t.Fatalf("yank: %q", got)
	}
	e.yankPop()
	if got := e.String(); got != "older|" {
		t.Fatalf("yankPop: %q", got)
	}
	e.yankPop() // wraps back to the newest
	if got := e.String(); got != "newer|" {
		t.Fatalf("yankPop wrap: %q", got)
	}
	// yankN picks the n-th most recent entry; out of range clamps to the
	// oldest
	e.reset([]rune(""))
	e.yankN(2)
	if got := e.String(); got != "older|" {
		t.Fatalf("yankN(2): %q", got)
	}
	e.reset([]rune(""))
	e.yankN(9)
	if got := e.String(); got != "older|" {
		t.Fatalf("yankN clamp: %q", got)
	}
}

func TestLineModelPrefixArgs(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "one two three")
	// alt+3 left moves three runes back
	mod, _ := m.Update(keyAlt("3"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("left"))
	m = mod.(*lineModel)
	if got := m.ed.String(); got != "one two th|ree" {
		t.Fatalf("alt+3 left: %q", got)
	}
	// ctrl+w with a prefix kills two words into one ring entry
	mod, _ = m.Update(keyAlt("2"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("ctrl+w"))
	m = mod.(*lineModel)
	if got := m.ed.String(); got != "one |ree" {
		t.Fatalf("alt+2 ctrl+w: %q", got)
	}
	// ctrl+y pastes the merged kill
	mod, _ = m.Update(keyName("ctrl+y"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "one thtwo ree" {
		t.Fatalf("ctrl+y: %q", got)
	}
	// ctrl+t at the end of the line swaps the last two runes; a repeat
	// argument of two oscillates back (readline semantics)
	m.ed.reset([]rune("abcd"))
	m.ed.moveEnd()
	mod, _ = m.Update(keyAlt("2"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("ctrl+t"))
	m = mod.(*lineModel)
	if got := string(m.ed.buf); got != "abcd" {
		t.Fatalf("alt+2 ctrl+t: %q", got)
	}
}

// keyAlt builds an alt-modified printable key message.
func keyAlt(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: true}
}

func TestInputMode(t *testing.T) {
	t.Setenv("USQL_INPUT", "tui")
	t.Setenv("TERM", "xterm-256color")
	if !inputMode(true, false, false) {
		t.Error("tui mode should activate when interactive")
	}
	if inputMode(false, false, false) {
		t.Error("tui mode must not activate when non-interactive")
	}
	if inputMode(true, true, false) {
		t.Error("tui mode must not activate when forced non-interactive")
	}
	if inputMode(true, false, true) {
		t.Error("tui mode must not activate under cygwin")
	}
	t.Setenv("USQL_INPUT", "readline")
	if inputMode(true, false, false) {
		t.Error("readline must stay the default engine")
	}
}

// fakeHL is an output filter standing in for chroma: it wraps non-empty
// input in a red SGR pair, preserving the printable content.
func fakeHL(s string) string {
	if s == "" {
		return s
	}
	return "\x1b[31m" + s + "\x1b[0m"
}

func TestSplitStyled(t *testing.T) {
	// mid-token split: the escape before the cursor belongs to the tail
	before, at, after := splitStyled("\x1b[36mSELECT\x1b[0m", 2)
	if before != "\x1b[36mSE" || at != "L" || after != "ECT\x1b[0m" {
		t.Fatalf("mid-token: %q %q %q", before, at, after)
	}
	// token-boundary split: the token-start escape precedes the rune
	before, at, after = splitStyled("\x1b[31mSE\x1b[0mLECT", 3)
	if before != "\x1b[31mSE\x1b[0mL" || at != "E" || after != "CT" {
		t.Fatalf("boundary: %q %q %q", before, at, after)
	}
	// multi-byte runes count as one printable rune each
	before, at, after = splitStyled("中文abc", 2)
	if before != "中文" || at != "a" || after != "bc" {
		t.Fatalf("multi-byte: %q %q %q", before, at, after)
	}
}

func TestStyledRuneLenAndLastSGR(t *testing.T) {
	if n := styledRuneLen("\x1b[31m中文\x1b[0mab"); n != 4 {
		t.Fatalf("styledRuneLen: %d", n)
	}
	if s := lastSGR("\x1b[36mSELECT\x1b[0m"); s != "" {
		t.Fatalf("lastSGR after reset: %q", s)
	}
	if s := lastSGR("\x1b[36mSE"); s != "\x1b[36m" {
		t.Fatalf("lastSGR mid-token: %q", s)
	}
}

func TestLineModelHighlight(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Prompt("> ")
	tr.SetOutput(fakeHL)
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "se")
	// the trailing re-highlight lands on hlMsg and re-renders fully styled
	mod, _ := m.Update(hlMsg{})
	m = mod.(*lineModel)
	if m.hlIn != "se" || m.hlOut != "\x1b[31mse\x1b[0m" {
		t.Fatalf("cache: %q %q", m.hlIn, m.hlOut)
	}
	if v := m.View(); !strings.Contains(v, "\x1b[31mse\x1b[0m") {
		t.Fatalf("view not highlighted: %q", v)
	}
	// mid-line cursor: the SGR state interrupted by the cursor cell is
	// re-issued so the tail keeps its styling
	mod, _ = m.Update(keyName("left"))
	m = mod.(*lineModel)
	v := m.View()
	if !strings.Contains(v, "\x1b[31ms") {
		t.Fatalf("mid-line view lost styling: %q", v)
	}
	// the final (post-enter) echo is highlighted as well
	mod, _ = m.Update(keyName("right"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("enter"))
	m = mod.(*lineModel)
	if !m.done {
		t.Fatal("enter must finalize")
	}
	if v := m.View(); !strings.Contains(v, "\x1b[31mse\x1b[0m") {
		t.Fatalf("final view not highlighted: %q", v)
	}
}

func TestLineModelHighlightDebounce(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Prompt("> ")
	tr.SetOutput(fakeHL)
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "se")
	// the second keystroke lands inside the debounce window: the cache
	// holds the first keystroke's render and a trailing tick is armed
	if m.hlIn != "s" || m.hlOut != "\x1b[31ms\x1b[0m" {
		t.Fatalf("cache: %q %q", m.hlIn, m.hlOut)
	}
	// a stale highlight of a different length must not drive the cursor
	// overlay: the mid-line render falls back to the plain buffer
	mod, _ := m.Update(keyTyped("x"))
	m = mod.(*lineModel)
	mod, _ = m.Update(keyName("left"))
	m = mod.(*lineModel)
	if v := m.View(); strings.Contains(v, "\x1b") {
		t.Fatalf("stale overlay must fall back to plain: %q", v)
	}
	// the trailing tick recomputes the current buffer
	mod, _ = m.Update(hlMsg{})
	m = mod.(*lineModel)
	if m.hlIn != "sex" || m.hlOut != "\x1b[31msex\x1b[0m" {
		t.Fatalf("trailing recompute: %q %q", m.hlIn, m.hlOut)
	}
}

func TestLineModelNoOutputFilter(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Prompt("> ")
	m := newLineModel(tr, "> ", -1)
	m = typeLine(m, "se")
	if v := m.View(); strings.Contains(v, "\x1b") {
		t.Fatalf("view must stay plain without a filter: %q", v)
	}
	if m.hlIn != "" || m.hlOut != "" {
		t.Fatalf("cache must stay empty without a filter: %q %q", m.hlIn, m.hlOut)
	}
}

func TestLineModelMenuPlacement(t *testing.T) {
	tr := newTUI(nil, nil, nil, "", nil)
	tr.Completer(&stubCompleter{cands: []string{"ect", "ection"}})
	tr.Prompt("> ")
	// unknown cursor row: legacy top-anchored shrink below
	m := newLineModel(tr, "> ", -1)
	mod, _ := m.Update(tea.WindowSizeMsg{Height: 6})
	m = mod.(*lineModel)
	if m.menuAbove || m.menuMax != 4 {
		t.Fatalf("unknown row: above=%v max=%d", m.menuAbove, m.menuMax)
	}
	// mid-screen: full menu below the input line
	m2 := newLineModel(tr, "> ", 10)
	mod, _ = m2.Update(tea.WindowSizeMsg{Height: 30})
	m2 = mod.(*lineModel)
	if m2.menuAbove || m2.menuMax != menuHeight {
		t.Fatalf("mid-screen: above=%v max=%d", m2.menuAbove, m2.menuMax)
	}
	// shallow space below: shrunk menu under the line
	m3 := newLineModel(tr, "> ", 24) // 30-24-1 = 5 rows below
	mod, _ = m3.Update(tea.WindowSizeMsg{Height: 30})
	m3 = mod.(*lineModel)
	if m3.menuAbove || m3.menuMax != 5 {
		t.Fatalf("shallow below: above=%v max=%d", m3.menuAbove, m3.menuMax)
	}
	// at the bottom: the menu pops above the input line
	m4 := newLineModel(tr, "> ", 28)
	mod, _ = m4.Update(tea.WindowSizeMsg{Height: 30})
	m4 = mod.(*lineModel)
	if !m4.menuAbove || m4.menuMax != menuHeight {
		t.Fatalf("at bottom: above=%v max=%d", m4.menuAbove, m4.menuMax)
	}
	// the rendered view places the menu above the input line
	m4 = typeLine(m4, "sel")
	mod, _ = m4.Update(keyName("tab"))
	m4 = mod.(*lineModel)
	if m4.menu == nil {
		t.Fatal("menu did not open")
	}
	v := m4.View()
	menuIdx := strings.Index(v, "select")
	lineIdx := strings.Index(v, "> sel")
	if menuIdx == -1 || lineIdx == -1 || menuIdx > lineIdx {
		t.Fatalf("menu not rendered above: menu@%d line@%d", menuIdx, lineIdx)
	}
}

func TestGuardedWriter(t *testing.T) {
	var out strings.Builder
	g := &guardedWriter{w: &out}
	// outside sessions: pass-through
	if _, err := g.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "a" {
		t.Fatalf("pass-through: %q", out.String())
	}
	// during a session: buffered, then flushed in order
	g.begin()
	g.Write([]byte("background "))
	g.Write([]byte("line\n"))
	if out.String() != "a" {
		t.Fatalf("write leaked during session: %q", out.String())
	}
	g.flush()
	if out.String() != "abackground line\n" {
		t.Fatalf("flush: %q", out.String())
	}
	// after flush: pass-through again
	g.Write([]byte("b"))
	if out.String() != "abackground line\nb" {
		t.Fatalf("post-flush: %q", out.String())
	}
}

func TestGuardedWriterConcurrent(t *testing.T) {
	var out strings.Builder
	g := &guardedWriter{w: &out}
	g.begin()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				g.Write([]byte{byte('0' + i)})
			}
		}(i)
	}
	g.flush()
	wg.Wait()
	// every write is accounted for exactly once
	if n := strings.Count(out.String(), ""); n != 8*50+1 {
		t.Fatalf("lost writes: %d runes, want %d", n-1, 8*50)
	}
}
